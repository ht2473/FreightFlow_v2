package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"freightflow/internal/ingest"
	"freightflow/internal/ingest/sources/basemap"
	"freightflow/internal/ingest/sources/mosdata"
	"freightflow/internal/ingest/sources/osm"
	"freightflow/internal/ingest/sources/rosstat"
	"freightflow/internal/ingest/sources/transport"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/outbound"
)

// newIngest создаёт сервис загрузок с источниками приложения и синхронизирует реестр.
func newIngest(ctx context.Context, env *Env, rt *runtime, client *jobs.Client) (*ingest.Service, error) {
	cfg := env.Config
	svc, err := ingest.NewService(ingest.Deps{
		Pool:  rt.pool,
		Blobs: rt.blobs,
		Jobs:  client,
		Log:   env.Log.With("component", "ingest"),
		Secrets: map[string]string{
			"FF_MOSDATA_API_KEY": cfg.MosDataAPIKey,
		},
		WorkDir:   filepath.Join(cfg.DataDir, "work"),
		RuCAFile:  cfg.RuCAFile,
		UserAgent: "FreightFlow/" + Version,
	}, dataSources(env)...)
	if err != nil {
		return nil, err
	}
	if err := svc.Sync(ctx); err != nil {
		return nil, err
	}
	return svc, nil
}

// dataSources — загрузчики, подключённые в приложении.
func dataSources(env *Env) []ingest.Source {
	cfg := env.Config
	out := []ingest.Source{
		osm.New(osm.Config{DatabaseURL: cfg.DatabaseURL, OutputDir: filepath.Join(cfg.DataDir, "osm")}),
		basemap.New(basemap.Config{DataDir: cfg.DataDir}),
	}
	for _, ds := range mosdata.Datasets {
		out = append(out, mosdata.New(ds))
	}
	out = append(out, rosstat.NewPopulation(), rosstat.NewFreight(), transport.NewCargo())
	return out
}

// mosdataClient — клиент портала открытых данных Москвы для служебных команд.
func mosdataClient(ctx context.Context, env *Env) (*mosdata.Client, error) {
	if env.Config.MosDataAPIKey == "" {
		return nil, fmt.Errorf("%w: FF_MOSDATA_API_KEY", ingest.ErrMissingSecret)
	}
	hc, err := outbound.New(ctx, outbound.Config{Hosts: []string{mosdata.Host}, UserAgent: "FreightFlow/" + Version})
	if err != nil {
		return nil, err
	}
	return mosdata.NewClient(hc, env.Config.MosDataAPIKey), nil
}

func openBlobs(env *Env) (*blob.Store, error) {
	return blob.Open(env.Config.DataDir)
}

// runETL — команды загрузок: список источников и запуск в текущем процессе.
func runETL(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		etlUsage(env)
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "etl")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	svc, err := newIngest(ctx, env, rt, nil)
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		return etlList(ctx, env, svc)
	case "inspect":
		// Паспорт, схема и первые записи набора портала Москвы: etl inspect <id> [записей].
		if len(args) < 2 {
			etlUsage(env)
			return ErrUsage
		}
		id, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("номер набора: %w", err)
		}
		rows := 2
		if len(args) > 2 {
			if rows, err = strconv.Atoi(args[2]); err != nil {
				return fmt.Errorf("число записей: %w", err)
			}
		}
		var fields []string
		if len(args) > 3 {
			fields = strings.Split(args[3], ",")
		}
		c, err := mosdataClient(ctx, env)
		if err != nil {
			return err
		}
		return mosdata.Inspect(ctx, c, id, rows, fields, env.Stdout)
	case "regions":
		// Многоугольники вырезки OSM из загруженных границ; запускается из корня репозитория.
		files, err := osm.ExportRegions(ctx, rt.pool, filepath.Join("etl", "regions"))
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Fprintln(env.Stdout, f)
		}
		return nil
	case "run":
		fs := flag.NewFlagSet("etl run", flag.ContinueOnError)
		fs.SetOutput(env.Stderr)
		force := fs.Bool("force", false, "загрузить, даже если снимок не изменился")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 {
			etlUsage(env)
			return ErrUsage
		}
		code := fs.Arg(0)
		run, err := svc.RunNow(ctx, code, *force)
		if errors.Is(err, ingest.ErrUnknownSource) {
			return fmt.Errorf("неизвестный источник %q (список: freightflow etl list)", code)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%s: %s", code, run.Status)
		if run.FinishedAt != nil && run.StartedAt != nil {
			fmt.Fprintf(env.Stdout, " за %s", run.FinishedAt.Sub(*run.StartedAt).Round(time.Second))
		}
		fmt.Fprintln(env.Stdout)
		for k, v := range run.Stats {
			fmt.Fprintf(env.Stdout, "  %s: %d\n", k, v)
		}
		if run.Status == ingest.StatusFailed {
			return fmt.Errorf("загрузка не удалась: %s", run.Error)
		}
		return nil
	default:
		etlUsage(env)
		return ErrUsage
	}
}

func etlList(ctx context.Context, env *Env, svc *ingest.Service) error {
	list, err := svc.ListSources(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ИСТОЧНИК\tНАЗВАНИЕ\tРАСПИСАНИЕ\tПОСЛЕДНИЙ ЗАПУСК\tУСПЕШНО")
	for _, s := range list {
		schedule := "вручную"
		if s.Interval > 0 {
			schedule = "каждые " + s.Interval.String()
		}
		if !s.Enabled {
			schedule += " (выключен)"
		}
		last, ok := "—", "—"
		if s.LastRun != nil {
			last = s.LastRun.Status + " " + s.LastRun.QueuedAt.Format("02.01 15:04")
		}
		if s.LastSuccessAt != nil {
			ok = s.LastSuccessAt.Format("02.01.2006 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Code, s.Title, schedule, last, ok)
	}
	return w.Flush()
}

func etlUsage(env *Env) {
	fmt.Fprintln(env.Stderr, "Использование: freightflow etl list | etl run [-force] <источник> | etl inspect <набор> [записей] [поле,поле] | etl regions")
}

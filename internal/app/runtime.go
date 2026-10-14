package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"freightflow/internal/admin"
	"freightflow/internal/facility"
	"freightflow/internal/identity"
	"freightflow/internal/ingest"
	"freightflow/internal/operations"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/telemetry"
	"freightflow/internal/report"
	"freightflow/internal/routing"
)

// runtime — общие службы долгоживущего процесса (app, worker).
type runtime struct {
	tel   *telemetry.Telemetry
	pool  *pgxpool.Pool
	bus   *events.Bus
	blobs *blob.Store
}

// startRuntime готовит каталог данных, телеметрию и базу. Телеметрия создаётся до пула:
// инструментирование запросов берёт глобальные провайдеры.
func startRuntime(ctx context.Context, env *Env, service string) (*runtime, error) {
	if err := os.MkdirAll(env.Config.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	tel, err := telemetry.New(ctx, telemetry.Config{
		Service: service, Version: Version,
		OTLPEndpoint: env.Config.OTLPEndpoint, SampleRatio: env.Config.TraceRatio,
	})
	if err != nil {
		return nil, err
	}
	pool, err := openDatabase(ctx, env)
	if err != nil {
		_ = tel.Shutdown(context.WithoutCancel(ctx))
		return nil, err
	}
	blobs, err := openBlobs(env)
	if err != nil {
		pool.Close()
		_ = tel.Shutdown(context.WithoutCancel(ctx))
		return nil, err
	}
	return &runtime{tel: tel, pool: pool, bus: events.NewBus(env.Log), blobs: blobs}, nil
}

func (rt *runtime) close(ctx context.Context) {
	_ = rt.blobs.Close()
	rt.pool.Close()
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = rt.tel.Shutdown(sctx)
}

// serveMetrics отдаёт /metrics на служебном адресе (не публикуется наружу).
func (rt *runtime) serveMetrics(ctx context.Context, env *Env, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", rt.tel.Handler())
	return httpx.Serve(ctx, env.Log, "metrics", addr, mux)
}

// modules — сервисы модулей, общие для app и worker.
type modules struct {
	identity   *identity.Service
	ingest     *ingest.Service
	facilities *facility.Service
	reports    *report.Service
	inserter   *jobs.Client
}

// newModules создаёт сервисы модулей. Постановка задач — через отдельный клиент очереди:
// исполняющему клиенту нужен реестр, а реестру — сервисы.
func newModules(ctx context.Context, env *Env, rt *runtime) (*modules, error) {
	ids, err := newIdentity(ctx, env, rt.pool)
	if err != nil {
		return nil, err
	}
	inserter, err := jobs.NewInserter(rt.pool, env.Log)
	if err != nil {
		return nil, err
	}
	ing, err := newIngest(ctx, env, rt, inserter)
	if err != nil {
		return nil, err
	}
	facilities := facility.New(rt.pool)
	return &modules{
		identity: ids, ingest: ing, facilities: facilities,
		reports: report.New(rt.pool, inserter, rt.blobs, facilities), inserter: inserter,
	}, nil
}

// registry собирает фоновые задачи модулей.
func (m *modules) registry(env *Env, pool *pgxpool.Pool) *jobs.Registry {
	reg := jobs.NewRegistry()
	admin.RegisterMaintenance(reg, pool, m.identity, env.Log)
	ingest.Register(reg, m.ingest)
	operations.Register(reg, pool, env.Log.With("component", "operations"))
	routing.Register(reg, pool, env.Config.ValhallaURL, env.Log.With("component", "routing"))
	report.Register(reg, m.reports, env.Log.With("component", "report"))
	return reg
}

// runWorkers исполняет фоновые задачи до отмены ctx и дожидается текущих при остановке.
func (rt *runtime) runWorkers(ctx context.Context, env *Env, reg *jobs.Registry) error {
	client, err := jobs.NewWorker(rt.pool, env.Log, reg, jobs.Observability{Meters: rt.tel.Meters, Tracers: rt.tel.Tracers})
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start workers: %w", err)
	}
	env.Log.Info("job workers started")
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := client.Stop(sctx); err != nil {
		return fmt.Errorf("stop workers: %w", err)
	}
	env.Log.Info("job workers stopped")
	return nil
}

// runWorker — отдельный процесс фоновых задач.
func runWorker(ctx context.Context, env *Env, _ []string) error {
	rt, err := startRuntime(ctx, env, "worker")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	mods, err := newModules(ctx, env, rt)
	if err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return rt.serveMetrics(gctx, env, env.Config.WorkerMetricsAddr) })
	g.Go(func() error { return events.Listen(gctx, rt.pool, rt.bus, env.Log) })
	g.Go(func() error { return rt.runWorkers(gctx, env, mods.registry(env, rt.pool)) })
	return g.Wait()
}

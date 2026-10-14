package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"freightflow/internal/routing"
)

// runRouting — граф и расчёты маршрутизации: prepare — наложение ограничений из знаков на
// выгрузку OSM (контейнер worker, нужен osmium; граф собирает Valhalla — task routing:build);
// accessibility — доступность объектов по текущему графу; analyze — сетевой анализ (значимость
// участков, узкие места по габаритам).
func runRouting(ctx context.Context, env *Env, args []string) error {
	if len(args) != 1 || (args[0] != "prepare" && args[0] != "accessibility" && args[0] != "analyze") {
		fmt.Fprintln(env.Stderr, "Использование: freightflow routing prepare | accessibility | analyze")
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "routing")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	if args[0] == "analyze" {
		svc := routing.NewService(rt.pool, routing.NewClient(env.Config.ValhallaURL))
		st, err := svc.AnalyzeNetwork(ctx, routing.DefaultCentrality, func(msg string) { fmt.Fprintln(env.Stdout, msg) })
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "сетевой анализ: версия %d за %s\n", st.Version, st.Duration.Round(time.Second))
		return nil
	}
	if args[0] == "accessibility" {
		svc := routing.NewService(rt.pool, routing.NewClient(env.Config.ValhallaURL))
		st, err := svc.RefreshAccessibility(ctx, func(msg string) { fmt.Fprintln(env.Stdout, msg) })
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "доступность: объектов %d, целей %d, профилей %d, запросов %d, недостижимо %d (граф %s)\n",
			st.Facilities, st.Targets, st.Profiles, st.Requests, st.Unreachable, st.GraphBuiltAt.Format("02.01.2006 15:04"))
		return nil
	}
	dir := filepath.Join(env.Config.DataDir, "routing")
	res, err := routing.Prepare(ctx, rt.pool,
		filepath.Join(env.Config.DataDir, "osm", "routing.osm.pbf"),
		filepath.Join(dir, "prepare"),
		filepath.Join(dir, "routing.osm.pbf"))
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "ограничения из знаков наложены на %d линий: %s\n", res.Ways, res.Output)
	return nil
}

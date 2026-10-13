package app

import (
	"context"
	"fmt"
	"path/filepath"

	"freightflow/internal/routing"
)

// runRouting — подготовка данных графа маршрутизации. Запускается в контейнере worker
// (нужен osmium); сборку графа выполняет Valhalla (task routing:build).
func runRouting(ctx context.Context, env *Env, args []string) error {
	if len(args) != 1 || args[0] != "prepare" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow routing prepare")
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "routing")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
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

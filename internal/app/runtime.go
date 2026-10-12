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
	"freightflow/internal/identity"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/telemetry"
)

// runtime — общие службы долгоживущего процесса (app, worker).
type runtime struct {
	tel  *telemetry.Telemetry
	pool *pgxpool.Pool
	bus  *events.Bus
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
	return &runtime{tel: tel, pool: pool, bus: events.NewBus(env.Log)}, nil
}

func (rt *runtime) close(ctx context.Context) {
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

// registry собирает фоновые задачи модулей.
func registry(env *Env, pool *pgxpool.Pool, ids *identity.Service) *jobs.Registry {
	reg := jobs.NewRegistry()
	admin.RegisterMaintenance(reg, pool, ids, env.Log)
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
	ids, err := newIdentity(ctx, env, rt.pool)
	if err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return rt.serveMetrics(gctx, env, env.Config.WorkerMetricsAddr) })
	g.Go(func() error { return events.Listen(gctx, rt.pool, rt.bus, env.Log) })
	g.Go(func() error { return rt.runWorkers(gctx, env, registry(env, rt.pool, ids)) })
	return g.Wait()
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"freightflow/internal/api"
	"freightflow/internal/identity"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/secret"
	"freightflow/internal/platform/session"
	"freightflow/internal/regulation"
	"freightflow/internal/routing"
	"freightflow/internal/tiles"
	"freightflow/internal/web"
)

func runServe(ctx context.Context, env *Env, args []string) error {
	cfg := env.Config
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	// В разработке задачи исполняются в том же процессе; в рабочем режиме — отдельным worker.
	withWorker := fs.Bool("worker", cfg.IsDev(), "исполнять фоновые задачи в этом процессе")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}

	rt, err := startRuntime(ctx, env, "app")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	pool := rt.pool

	mods, err := newModules(ctx, env, rt)
	if err != nil {
		return err
	}
	ids := mods.identity
	sessions := session.New(pool, !cfg.IsDev())
	mailer := mail.NewSender(mail.Config{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, User: cfg.SMTP.User, Password: cfg.SMTP.Password, From: cfg.SMTP.From,
	})

	tileLog := env.Log.With("component", "tiles")
	layers := tiles.NewService(pool, mods.ingest, tileLog)
	bm := tiles.NewBasemap(cfg.DataDir, tileLog)
	defer bm.Close()
	mapHandler := tiles.New(tiles.Deps{Basemap: bm, Data: layers, Auth: tiles.NewAuth(ids, sessions), Log: tileLog})

	apiHandler, err := api.New(api.Deps{
		Identity: ids, Ingest: mods.ingest, Layers: layers, Regulation: regulation.New(pool),
		Routing: routing.NewService(pool, routing.NewClient(cfg.ValhallaURL)), Sessions: sessions, Mailer: mailer,
		BaseURL: cfg.BaseURL, Log: env.Log, Meters: rt.tel.Meters, Tracers: rt.tel.Tracers,
	})
	if err != nil {
		return err
	}
	webHandler, err := web.New(web.Deps{
		Identity: ids, Sessions: sessions, Mailer: mailer, BaseURL: cfg.BaseURL, Dev: cfg.IsDev(), DevVite: cfg.DevViteURL, Log: env.Log,
	})
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz())
	mux.Handle("GET /readyz", readyz(pool))
	mux.Handle("/api/v1/", apiHandler)
	mux.Handle("/map/", mapHandler)
	mux.Handle("/tiles/", mapHandler)
	mux.Handle("/", webHandler)

	crossOrigin, err := httpx.CrossOrigin()
	if err != nil {
		return err
	}
	observe, err := httpx.Observe(rt.tel.Meters, rt.tel.Tracers)
	if err != nil {
		return err
	}
	handler := httpx.Chain(httpx.Routes(mux),
		httpx.WithRequestID(),
		observe,
		httpx.Recover(env.Log),
		httpx.AccessLog(env.Log),
		httpx.SecurityHeaders(!cfg.IsDev()),
		crossOrigin,
		httpx.WithClient(),
		sessions.LoadAndSave,
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return httpx.Serve(gctx, env.Log, "app", cfg.HTTPAddr, handler) })
	g.Go(func() error { return rt.serveMetrics(gctx, env, cfg.MetricsAddr) })
	g.Go(func() error { return events.Listen(gctx, pool, rt.bus, env.Log) })
	g.Go(func() error { return layers.Watch(gctx, rt.bus) })
	if *withWorker {
		g.Go(func() error { return rt.runWorkers(gctx, env, mods.registry(env, pool)) })
	}
	return g.Wait()
}

// newIdentity создаёт сервис доступа и синхронизирует встроенные роли.
func newIdentity(ctx context.Context, env *Env, pool *pgxpool.Pool) (*identity.Service, error) {
	if env.Config.EncryptionKey == "" {
		return nil, errors.New("FF_ENCRYPTION_KEY is required (openssl rand -hex 32)")
	}
	box, err := secret.NewBox(env.Config.EncryptionKey)
	if err != nil {
		return nil, err
	}
	ids := identity.NewService(pool, box)
	if err := ids.SyncBuiltinRoles(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

func healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
}

type readiness struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// readyz проверяет зависимости, без которых сервер не может обслуживать запросы.
func readyz(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		res := readiness{Status: "ok", Checks: map[string]string{"database": "ok"}}
		code := http.StatusOK
		if err := pool.Ping(ctx); err != nil {
			res.Status = "unavailable"
			res.Checks["database"] = "unavailable"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(res)
	})
}

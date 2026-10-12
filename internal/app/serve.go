package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"freightflow/internal/platform/httpx"
)

func runServe(ctx context.Context, env *Env, _ []string) error {
	pool, err := openDatabase(ctx, env)
	if err != nil {
		return err
	}
	defer pool.Close()

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz())
	mux.Handle("GET /readyz", readyz(pool))

	crossOrigin, err := httpx.CrossOrigin()
	if err != nil {
		return err
	}
	handler := httpx.Chain(mux,
		httpx.WithRequestID(),
		httpx.Recover(env.Log),
		httpx.AccessLog(env.Log),
		httpx.SecurityHeaders(!env.Config.IsDev()),
		crossOrigin,
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return httpx.Serve(gctx, env.Log, "app", env.Config.HTTPAddr, handler) })
	return g.Wait()
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

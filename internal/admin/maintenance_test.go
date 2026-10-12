package admin_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"freightflow/internal/admin"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/jobs"
)

type tokens struct{ calls int }

func (t *tokens) DeleteStaleTokens(context.Context) (int64, error) {
	t.calls++
	return 0, nil
}

func TestCleanupRunsThroughQueue(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Истёкший сеанс и старое событие должны быть удалены, свежие — остаться.
	if _, err := pool.Exec(ctx, `INSERT INTO core.session (token, data, expiry) VALUES
		('expired', '\x00', now() - interval '1 hour'), ('alive', '\x00', now() + interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	if err := events.Publish(ctx, pool, events.JobProgress, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notify.outbox SET created_at = now() - interval '30 days'`); err != nil {
		t.Fatal(err)
	}

	tk := &tokens{}
	reg := jobs.NewRegistry()
	jobs.Register(reg, &admin.CleanupWorker{Pool: pool, Tokens: tk, Log: log})
	client, err := jobs.NewWorker(pool, log, reg, jobs.Observability{Meters: noop.NewMeterProvider(), Tracers: tracenoop.NewTracerProvider()})
	if err != nil {
		t.Fatal(err)
	}
	completed, cancelSub := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancelSub()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(sctx)
	})

	if _, err := client.Insert(ctx, admin.CleanupArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-completed:
		if ev.Kind != river.EventKindJobCompleted {
			t.Fatalf("job failed: %+v", ev.Job.Errors)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cleanup job did not complete")
	}

	var sessions, outbox int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.session), (SELECT count(*) FROM notify.outbox)`).Scan(&sessions, &outbox); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || outbox != 0 || tk.calls != 1 {
		t.Fatalf("sessions=%d outbox=%d token cleanups=%d", sessions, outbox, tk.calls)
	}
}

package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/events"
)

func TestPublishedEventsReachSubscribers(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus(log)
	works := bus.Subscribe(8, events.RoadworkChanged)
	defer works.Close()
	all := bus.Subscribe(8)
	defer all.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() { done <- events.Listen(ctx, pool, bus, log, events.OnReady(func() { close(ready) })) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("listen: %v", err)
		}
	})
	<-ready

	// Откат транзакции — события нет.
	errRollback := errors.New("rollback")
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if err := events.Publish(ctx, tx, events.RoadworkChanged, map[string]string{"id": "rolled-back"}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("tx: %v", err)
	}

	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if err := events.Publish(ctx, tx, events.DataPublished, map[string]string{"domain": "facility"}); err != nil {
			return err
		}
		return events.Publish(ctx, tx, events.RoadworkChanged, map[string]string{"id": "42"})
	})
	if err != nil {
		t.Fatal(err)
	}

	got := receive(t, works.C)
	var payload map[string]string
	if err := json.Unmarshal(got.Payload, &payload); err != nil || payload["id"] != "42" {
		t.Fatalf("roadwork payload: %s %v", got.Payload, err)
	}
	first, second := receive(t, all.C), receive(t, all.C)
	if first.Type != events.DataPublished || second.Type != events.RoadworkChanged || first.ID >= second.ID {
		t.Fatalf("order: %+v, %+v", first, second)
	}

	select {
	case e := <-works.C:
		t.Fatalf("unexpected event: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDeleteOlderThan(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := context.Background()
	if err := events.Publish(ctx, pool, events.JobProgress, map[string]int{"done": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notify.outbox SET created_at = now() - interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if err := events.Publish(ctx, pool, events.JobProgress, map[string]int{"done": 2}); err != nil {
		t.Fatal(err)
	}
	n, err := events.DeleteOlderThan(ctx, pool, 7*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
}

func receive(t *testing.T, c <-chan events.Event) events.Event {
	t.Helper()
	select {
	case e := <-c:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
		return events.Event{}
	}
}

package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/events"
)

// recorder запоминает полученные события; fail — ошибка обработчика для следующей порции.
type recorder struct {
	got  []string
	fail error
}

func (r *recorder) handle(_ context.Context, _ pgx.Tx, evs []events.Event) error {
	if r.fail != nil {
		return r.fail
	}
	for _, e := range evs {
		r.got = append(r.got, string(e.Payload))
	}
	return nil
}

func publish(t *testing.T, pool *pgxpool.Pool, typ, id string) {
	t.Helper()
	err := db.InTx(context.Background(), pool, func(tx pgx.Tx) error {
		return events.Publish(context.Background(), tx, typ, map[string]string{"id": id})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// drain выполняет шаги, пока не придут want событий: границу снимка могут задерживать
// транзакции параллельных тестов того же сервера.
func drain(t *testing.T, pool *pgxpool.Pool, c events.Consumer, r *recorder, want int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for len(r.got) < want {
		if _, err := c.Step(context.Background(), pool); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d events %v, want %d", len(r.got), r.got, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestConsumerDeliversEachEventOnceInCommitOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.New(t)

	r := &recorder{}
	c := events.Consumer{Name: "test", Types: []string{events.RoadworkChanged}, Handle: r.handle}
	if err := c.Register(ctx, pool); err != nil {
		t.Fatal(err)
	}

	// Транзакция A получает меньший номер, но фиксируется после B: потребитель не должен
	// пропустить её событие, прочитав B.
	txA, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Publish(ctx, txA, events.RoadworkChanged, map[string]string{"id": "a"}); err != nil {
		t.Fatal(err)
	}
	publish(t, pool, events.DataPublished, "other-type")
	publish(t, pool, events.RoadworkChanged, "b")
	if _, err := c.Step(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if len(r.got) != 0 {
		t.Fatalf("events read past an open transaction: %v", r.got)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	drain(t, pool, c, r, 2)
	if r.got[0] != `{"id": "a"}` || r.got[1] != `{"id": "b"}` {
		t.Fatalf("got %v, want a then b (commit order of transactions)", r.got)
	}

	// Ошибка обработчика: порция откатывается и приходит снова.
	publish(t, pool, events.RoadworkChanged, "c")
	r.fail = errors.New("handler down")
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := c.Step(ctx, pool); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("handler error was not returned")
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.fail = nil
	drain(t, pool, c, r, 3)
	if r.got[2] != `{"id": "c"}` {
		t.Fatalf("redelivered %v, want c", r.got[2:])
	}

	// После перезапуска (новое значение того же потребителя) позиция сохраняется.
	again := &recorder{}
	c2 := events.Consumer{Name: "test", Types: []string{events.RoadworkChanged}, Handle: again.handle}
	if err := c2.Register(ctx, pool); err != nil {
		t.Fatal(err)
	}
	publish(t, pool, events.RoadworkChanged, "d")
	drain(t, pool, c2, again, 1)
	if len(again.got) != 1 || again.got[0] != `{"id": "d"}` {
		t.Fatalf("after restart got %v, want only d", again.got)
	}
}

func TestConsumerRequiresRegistration(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	c := events.Consumer{Name: "missing", Handle: (&recorder{}).handle}
	if _, err := c.Step(context.Background(), pool); err == nil {
		t.Fatal("step of an unregistered consumer succeeded")
	}
}

package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"freightflow/internal/platform/db"
)

// Handler обрабатывает порцию событий в транзакции, в которой сдвигается позиция потребителя:
// изменения обработчика и новая позиция фиксируются вместе. Задачи очереди ставятся в той же
// транзакции (jobs.Client.InsertTx). Ошибка откатывает порцию, она будет прочитана снова.
type Handler func(ctx context.Context, tx pgx.Tx, evs []Event) error

// Consumer — постоянный потребитель событий. В отличие от подписки на шину, он не теряет
// событий: позиция хранится в notify.consumer, после перезапуска чтение продолжается с неё.
// Новый потребитель начинает с границы снимка: события транзакций, не завершённых к моменту
// регистрации, он прочитает (и, возможно, несколько только что завершённых). Порции одного
// потребителя обрабатываются по очереди во всех процессах (блокировка строки позиции).
type Consumer struct {
	Name   string
	Types  []string // без типов — все события
	Handle Handler
}

const (
	consumerBatch = 200
	// consumerPoll — интервал проверки без уведомлений: события транзакций, завершившихся
	// после длинной соседней транзакции, становятся доступны без нового NOTIFY.
	consumerPoll = 15 * time.Second
)

// RunConsumers обрабатывает события потребителями, пока не отменён ctx. Уведомления шины
// процесса будят потребителей сразу, без них — проверка раз в consumerPoll.
func RunConsumers(ctx context.Context, pool *pgxpool.Pool, bus *Bus, log *slog.Logger, cs ...Consumer) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, c := range cs {
		if c.Name == "" || c.Handle == nil {
			return errors.New("events: consumer needs a name and a handler")
		}
		g.Go(func() error { return c.run(gctx, pool, bus, log.With("consumer", c.Name)) })
	}
	return g.Wait()
}

func (c Consumer) run(ctx context.Context, pool *pgxpool.Pool, bus *Bus, log *slog.Logger) error {
	if err := c.Register(ctx, pool); err != nil {
		return err
	}
	wake := bus.Subscribe(1, c.Types...)
	defer wake.Close()
	tick := time.NewTicker(consumerPoll)
	defer tick.Stop()
	backoff := time.Second
	for {
		n, err := c.Step(ctx, pool)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			log.Error("event consumer failed, retrying", "error", err, "after", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, time.Minute)
			continue
		case n == consumerBatch:
			continue // в журнале есть ещё
		}
		backoff = time.Second
		select {
		case <-ctx.Done():
			return nil
		case <-wake.C:
		case <-tick.C:
		}
	}
}

// Register создаёт позицию нового потребителя на границе снимка; у существующего не меняет.
func (c Consumer) Register(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO notify.consumer (name, last_xid, last_id)
		VALUES ($1, pg_snapshot_xmin(pg_current_snapshot()), 0)
		ON CONFLICT (name) DO NOTHING`, c.Name)
	if err != nil {
		return fmt.Errorf("register consumer %s: %w", c.Name, err)
	}
	return nil
}

// Step обрабатывает одну порцию и возвращает число прочитанных записей журнала (с событиями
// чужих типов). Читаются только события транзакций, завершённых к началу запроса.
func (c Consumer) Step(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	n := 0
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		var xid string
		var id int64
		err := tx.QueryRow(ctx, `SELECT last_xid::text, last_id FROM notify.consumer WHERE name = $1 FOR UPDATE`, c.Name).
			Scan(&xid, &id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("consumer %s is not registered", c.Name)
		}
		if err != nil {
			return fmt.Errorf("lock consumer %s: %w", c.Name, err)
		}
		rows, err := tx.Query(ctx, `
			SELECT id, type, payload, created_at, xid::text FROM notify.outbox
			WHERE (xid, id) > ($1::xid8, $2) AND xid < pg_snapshot_xmin(pg_current_snapshot())
			ORDER BY xid, id LIMIT $3`, xid, id, consumerBatch)
		if err != nil {
			return fmt.Errorf("read events for %s: %w", c.Name, err)
		}
		var evs []Event
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.ID, &e.Type, &e.Payload, &e.CreatedAt, &xid); err != nil {
				rows.Close()
				return fmt.Errorf("scan event: %w", err)
			}
			id = e.ID
			n++
			if len(c.Types) == 0 || slices.Contains(c.Types, e.Type) {
				evs = append(evs, e)
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read events for %s: %w", c.Name, err)
		}
		if n == 0 {
			return nil
		}
		if len(evs) > 0 {
			if err := c.Handle(ctx, tx, evs); err != nil {
				return fmt.Errorf("consumer %s: %w", c.Name, err)
			}
		}
		_, err = tx.Exec(ctx, `UPDATE notify.consumer SET last_xid = $2::xid8, last_id = $3, updated_at = now() WHERE name = $1`,
			c.Name, xid, id)
		if err != nil {
			return fmt.Errorf("advance consumer %s: %w", c.Name, err)
		}
		return nil
	})
	return n, err
}

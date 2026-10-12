// Package events — доменные события. Событие пишется в notify.outbox в той же транзакции,
// что и изменение; после фиксации NOTIFY сообщает его номер. Слушатель в каждом процессе
// читает новые записи по порядку и раздаёт подписчикам.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel — канал NOTIFY для событий.
const Channel = "ff_events"

// Типы событий (03-architecture.md §18).
const (
	DataPublished       = "data.published"
	IngestRunFinished   = "ingest.run_finished"
	RoadworkChanged     = "roadwork.changed"
	RestrictionChanged  = "restriction.changed"
	AlertCreated        = "alert.created"
	AlertUpdated        = "alert.updated"
	NotificationCreated = "notification.created"
	JobProgress         = "job.progress"
	ReportReady         = "report.ready"
	ScenarioComputed    = "scenario.computed"
)

// Event — запись журнала событий.
type Event struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Execer — транзакция или пул, в котором выполняется изменение.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Publish записывает событие. Вызывается в транзакции изменения: если она откатится,
// события не будет; NOTIFY доставляется только после фиксации.
func Publish(ctx context.Context, tx Execer, typ string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode event %s: %w", typ, err)
	}
	_, err = tx.Exec(ctx, `
		WITH e AS (INSERT INTO notify.outbox (type, payload) VALUES ($1, $2) RETURNING id)
		SELECT pg_notify('`+Channel+`', id::text) FROM e`, typ, b)
	if err != nil {
		return fmt.Errorf("publish event %s: %w", typ, err)
	}
	return nil
}

// Subscription — подписка на события выбранных типов.
type Subscription struct {
	C      <-chan Event
	ch     chan Event
	types  []string
	bus    *Bus
	closed bool
}

// Close отменяет подписку.
func (s *Subscription) Close() { s.bus.unsubscribe(s) }

// Bus раздаёт события подписчикам процесса. Медленный подписчик не задерживает
// остальных: событие, не поместившееся в его буфер, пропускается.
type Bus struct {
	mu   sync.Mutex
	subs map[*Subscription]struct{}
	log  *slog.Logger
}

// NewBus создаёт шину.
func NewBus(log *slog.Logger) *Bus {
	return &Bus{subs: map[*Subscription]struct{}{}, log: log}
}

// Subscribe подписывает на события указанных типов; без типов — на все.
func (b *Bus) Subscribe(buffer int, types ...string) *Subscription {
	ch := make(chan Event, buffer)
	s := &Subscription{C: ch, ch: ch, types: types, bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (b *Bus) unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(b.subs, s)
	close(s.ch)
}

// Dispatch передаёт событие подписчикам.
func (b *Bus) Dispatch(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if len(s.types) > 0 && !slices.Contains(s.types, e.Type) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			b.log.Warn("event subscriber is slow, event dropped", "type", e.Type, "id", e.ID)
		}
	}
}

// ListenOption настраивает слушателя.
type ListenOption func(*listenConfig)

type listenConfig struct{ onReady func() }

// OnReady вызывается один раз, когда слушатель подписан и события после этого момента
// гарантированно будут доставлены.
func OnReady(fn func()) ListenOption { return func(c *listenConfig) { c.onReady = fn } }

// Listen слушает NOTIFY и раздаёт новые события, пока не отменён ctx. События читаются
// из таблицы по возрастанию номера, поэтому после переподключения пропусков нет.
func Listen(ctx context.Context, pool *pgxpool.Pool, bus *Bus, log *slog.Logger, opts ...ListenOption) error {
	var cfg listenConfig
	for _, o := range opts {
		o(&cfg)
	}
	var last int64
	if err := pool.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM notify.outbox`).Scan(&last); err != nil {
		return fmt.Errorf("read last event id: %w", err)
	}
	if cfg.onReady != nil {
		// Начальная позиция зафиксирована: всё, что запишется позже, будет прочитано.
		cfg.onReady()
	}
	backoff := time.Second
	for {
		err := listenOnce(ctx, pool, bus, &last)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // остановка процесса
		}
		log.Warn("event listener reconnecting", "error", err, "after", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, bus *Bus, last *int64) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// Догоняем события, записанные, пока соединения не было.
	if err := deliver(ctx, conn.Conn(), bus, last); err != nil {
		return err
	}
	for {
		// Разорванное соединение пул сам закроет при возврате.
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return fmt.Errorf("wait notification: %w", err)
		}
		if err := deliver(ctx, conn.Conn(), bus, last); err != nil {
			return err
		}
	}
}

const deliverBatch = 1000

func deliver(ctx context.Context, conn *pgx.Conn, bus *Bus, last *int64) error {
	for {
		rows, err := conn.Query(ctx,
			`SELECT id, type, payload, created_at FROM notify.outbox WHERE id > $1 ORDER BY id LIMIT $2`,
			*last, deliverBatch)
		if err != nil {
			return fmt.Errorf("read events: %w", err)
		}
		evs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Event])
		if err != nil {
			return fmt.Errorf("scan events: %w", err)
		}
		for _, e := range evs {
			bus.Dispatch(e)
			*last = e.ID
		}
		if len(evs) < deliverBatch {
			return nil
		}
	}
}

// DeleteOlderThan удаляет записи журнала событий старше age (задача обслуживания).
func DeleteOlderThan(ctx context.Context, pool *pgxpool.Pool, age time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM notify.outbox WHERE created_at < now() - make_interval(secs => $1)`, age.Seconds())
	if err != nil {
		return 0, fmt.Errorf("delete old events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Package admin — обслуживание системы: периодическая очистка, состояние служб, настройки.
package admin

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/events"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/session"
)

// eventRetention — сколько хранится журнал событий: подписчикам он нужен для догоняющего
// чтения после перезапуска, история изменений хранится в доменных таблицах.
const eventRetention = 7 * 24 * time.Hour

// TokenCleaner удаляет устаревшие одноразовые ссылки.
type TokenCleaner interface {
	DeleteStaleTokens(ctx context.Context) (int64, error)
}

// CleanupArgs — задача очистки устаревших записей.
type CleanupArgs struct{}

// Kind — вид задачи.
func (CleanupArgs) Kind() string { return "maintenance.cleanup" }

// InsertOpts — очередь обслуживания; одна задача за период.
func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       jobs.QueueMaintenance,
		UniqueOpts:  river.UniqueOpts{ByPeriod: time.Hour},
		MaxAttempts: 3,
	}
}

// CleanupWorker удаляет истёкшие сеансы, старые события и одноразовые ссылки.
type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	Pool   *pgxpool.Pool
	Tokens TokenCleaner
	Log    *slog.Logger
}

// Work выполняет очистку.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	sessions, err := session.DeleteExpired(ctx, w.Pool)
	if err != nil {
		return err
	}
	evs, err := events.DeleteOlderThan(ctx, w.Pool, eventRetention)
	if err != nil {
		return err
	}
	tokens, err := w.Tokens.DeleteStaleTokens(ctx)
	if err != nil {
		return err
	}
	w.Log.InfoContext(ctx, "maintenance cleanup", "sessions", sessions, "events", evs, "tokens", tokens)
	return nil
}

// RegisterMaintenance добавляет задачи обслуживания в реестр.
func RegisterMaintenance(reg *jobs.Registry, pool *pgxpool.Pool, tokens TokenCleaner, log *slog.Logger) {
	jobs.Register(reg, &CleanupWorker{Pool: pool, Tokens: tokens, Log: log})
	reg.Every(time.Hour, CleanupArgs{}, nil, true)
}

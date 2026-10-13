// Package operations — оперативная обстановка: дорожные работы и перекрытия, их жизненный
// цикл и ежедневные агрегаты. Записи работ загружают наборы портала Москвы (модуль ingest).
package operations

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/events"
	"freightflow/internal/platform/jobs"
)

// RefreshArgs — пересчёт состояний работ по текущей дате и снимок дня.
type RefreshArgs struct{}

// Kind — вид задачи.
func (RefreshArgs) Kind() string { return "operations.refresh" }

// RefreshWorker переводит работы в «идёт» и «завершена» по датам и обновляет снимок дня.
type RefreshWorker struct {
	river.WorkerDefaults[RefreshArgs]
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

// Work выполняет пересчёт в одной транзакции.
func (w *RefreshWorker) Work(ctx context.Context, _ *river.Job[RefreshArgs]) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	if err := tx.QueryRow(ctx, `SELECT operations.refresh_work_states()`).Scan(&n); err != nil {
		return fmt.Errorf("refresh work states: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT operations.take_daily_snapshot((now() AT TIME ZONE 'Europe/Moscow')::date)`); err != nil {
		return fmt.Errorf("daily snapshot: %w", err)
	}
	if n > 0 {
		if err := events.Publish(ctx, tx, events.RoadworkChanged, map[string]any{"stats": map[string]int{"state_transitions": n}}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.Log.InfoContext(ctx, "road work states refreshed", "transitions", n)
	return nil
}

// Register добавляет ежечасный пересчёт состояний работ.
func Register(reg *jobs.Registry, pool *pgxpool.Pool, log *slog.Logger) {
	jobs.Register(reg, &RefreshWorker{Pool: pool, Log: log})
	reg.Every(RefreshArgs{}.Kind(), time.Hour, RefreshArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}, true)
}

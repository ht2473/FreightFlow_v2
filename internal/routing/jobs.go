package routing

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// AccessibilityArgs — пересчёт доступности объектов, если граф маршрутизации новее расчёта.
type AccessibilityArgs struct{}

// Kind — вид задачи.
func (AccessibilityArgs) Kind() string { return "routing.accessibility" }

// AccessibilityWorker пересчитывает доступность после сборки нового графа.
type AccessibilityWorker struct {
	river.WorkerDefaults[AccessibilityArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — расчёт по всем профилям идёт около часа.
func (w *AccessibilityWorker) Timeout(*river.Job[AccessibilityArgs]) time.Duration {
	return 4 * time.Hour
}

// Work сравнивает время сборки графа со временем последнего расчёта; служба недоступна —
// задача завершается без ошибки (граф не собран или служба не запущена).
func (w *AccessibilityWorker) Work(ctx context.Context, _ *river.Job[AccessibilityArgs]) error {
	st, err := w.Service.vh.Status(ctx)
	if errors.Is(err, ErrUnavailable) {
		w.Log.InfoContext(ctx, "routing unavailable, accessibility skipped")
		return nil
	}
	if err != nil {
		return err
	}
	built := time.Unix(st.Tileset, 0)
	var last *time.Time
	if err := w.Service.pool.QueryRow(ctx, `SELECT max(graph_built_at) FROM analytics.accessibility`).Scan(&last); err != nil {
		return err
	}
	if last != nil && !built.After(*last) {
		return nil
	}
	res, err := w.Service.RefreshAccessibility(ctx, nil)
	if err != nil {
		return err
	}
	w.Log.InfoContext(ctx, "accessibility refreshed", "facilities", res.Facilities, "requests", res.Requests,
		"unreachable", res.Unreachable, "graph_built_at", built)
	return nil
}

// Register добавляет ежечасную проверку графа и пересчёт доступности.
func Register(reg *jobs.Registry, pool *pgxpool.Pool, valhallaURL string, log *slog.Logger) {
	jobs.Register(reg, &AccessibilityWorker{Service: NewService(pool, NewClient(valhallaURL)), Log: log})
	reg.Every(AccessibilityArgs{}.Kind(), time.Hour, AccessibilityArgs{},
		&river.InsertOpts{Queue: jobs.QueueRouting, UniqueOpts: river.UniqueOpts{ByArgs: true}}, false)
}

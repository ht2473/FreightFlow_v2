package analytics

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// RecomputeArgs — пересчёт показателей, входы которых изменились.
type RecomputeArgs struct{}

// Kind — вид задачи.
func (RecomputeArgs) Kind() string { return "analytics.recompute" }

// RecomputeWorker сверяет подписи входов и пересчитывает изменившиеся показатели в порядке
// зависимостей.
type RecomputeWorker struct {
	river.WorkerDefaults[RecomputeArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — показатели по сети для всех территорий считаются минутами.
func (w *RecomputeWorker) Timeout(*river.Job[RecomputeArgs]) time.Duration { return time.Hour }

// Work пересчитывает изменившиеся показатели.
func (w *RecomputeWorker) Work(ctx context.Context, _ *river.Job[RecomputeArgs]) error {
	res, err := w.Service.Recompute(ctx, nil, false)
	for _, r := range res {
		if !r.Skipped {
			w.Log.InfoContext(ctx, "metric computed", "metric", r.Code, "rows", r.Rows, "duration", r.Duration)
		}
	}
	return err
}

// Register добавляет пересчёт: при запуске и каждые 5 минут (сверка подписей дешёвая).
func Register(reg *jobs.Registry, svc *Service, log *slog.Logger) {
	jobs.Register(reg, &RecomputeWorker{Service: svc, Log: log})
	opts := &river.InsertOpts{Queue: jobs.QueueAnalytics, UniqueOpts: river.UniqueOpts{ByArgs: true}}
	reg.Every(RecomputeArgs{}.Kind(), 5*time.Minute, RecomputeArgs{}, opts, true)
}

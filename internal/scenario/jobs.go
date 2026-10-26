package scenario

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// RunWorker считает сценарии.
type RunWorker struct {
	river.WorkerDefaults[RunArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — сценарий с четырьмя видами целей и изохронами считается несколько минут.
func (w *RunWorker) Timeout(*river.Job[RunArgs]) time.Duration { return 30 * time.Minute }

// Work считает сценарий; неудача записывается в сценарий, задача не повторяется.
func (w *RunWorker) Work(ctx context.Context, job *river.Job[RunArgs]) error {
	start := time.Now()
	if err := w.Service.run(ctx, job.Args.ID); err != nil {
		w.Log.WarnContext(ctx, "scenario failed", "id", job.Args.ID, "err", err)
		return river.JobCancel(err)
	}
	w.Log.InfoContext(ctx, "scenario computed", "id", job.Args.ID, "duration", time.Since(start))
	return nil
}

// Register добавляет обработчики модуля и ежечасную проверку пакета устойчивости (считается
// заново, только если сменились граф маршрутизации или сетевой анализ).
func Register(reg *jobs.Registry, svc *Service, log *slog.Logger) {
	jobs.Register(reg, &RunWorker{Service: svc, Log: log})
	jobs.Register(reg, &ResilienceWorker{Service: svc, Log: log})
	reg.Every(ResilienceArgs{}.Kind(), time.Hour, ResilienceArgs{}, resilienceOpts(), true)
}

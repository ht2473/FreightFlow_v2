package report

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// ExportArgs — формирование файла выгрузки.
type ExportArgs struct {
	ID uuid.UUID `json:"id"`
}

// Kind — вид задачи.
func (ExportArgs) Kind() string { return "report.export" }

// ExportWorker формирует файлы выгрузок.
type ExportWorker struct {
	river.WorkerDefaults[ExportArgs]
	Service *Service
}

// Timeout — выгрузка до MaxExport строк.
func (w *ExportWorker) Timeout(*river.Job[ExportArgs]) time.Duration { return 10 * time.Minute }

// Work формирует файл; ошибка записывается в выгрузку, задача не повторяется.
func (w *ExportWorker) Work(ctx context.Context, job *river.Job[ExportArgs]) error {
	if err := w.Service.run(ctx, job.Args.ID); err != nil {
		return river.JobCancel(err)
	}
	return nil
}

// CleanupArgs — удаление просроченных выгрузок.
type CleanupArgs struct{}

// Kind — вид задачи.
func (CleanupArgs) Kind() string { return "report.cleanup" }

// CleanupWorker удаляет просроченные выгрузки и их файлы.
type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	Service *Service
	Log     *slog.Logger
}

// Work удаляет просроченное.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	n, err := w.Service.cleanup(ctx)
	if err == nil && n > 0 {
		w.Log.InfoContext(ctx, "expired exports removed", "files", n)
	}
	return err
}

// RenderArgs — формирование файла отчёта.
type RenderArgs struct {
	ID uuid.UUID `json:"id"`
}

// Kind — вид задачи.
func (RenderArgs) Kind() string { return "report.render" }

// RenderWorker формирует отчёты в службе pdf.
type RenderWorker struct {
	river.WorkerDefaults[RenderArgs]
	Service *Service
}

// Timeout — страница отчёта с картами и графиками печатается до пары минут.
func (w *RenderWorker) Timeout(*river.Job[RenderArgs]) time.Duration { return 5 * time.Minute }

// Work формирует отчёт; неудача записывается в отчёт, служба pdf недоступна — повтор.
func (w *RenderWorker) Work(ctx context.Context, job *river.Job[RenderArgs]) error {
	return w.Service.render(ctx, job.Args.ID)
}

// ScheduleArgs — запуск отчётов по расписаниям.
type ScheduleArgs struct{}

// Kind — вид задачи.
func (ScheduleArgs) Kind() string { return "report.schedules" }

// ScheduleWorker ставит отчёты расписаний, срок которых наступил.
type ScheduleWorker struct {
	river.WorkerDefaults[ScheduleArgs]
	Service *Service
	Log     *slog.Logger
}

// Work ставит отчёты.
func (w *ScheduleWorker) Work(ctx context.Context, _ *river.Job[ScheduleArgs]) error {
	n, err := w.Service.RunDue(ctx, time.Now())
	if err == nil && n > 0 {
		w.Log.InfoContext(ctx, "scheduled reports queued", "reports", n)
	}
	return err
}

// Register добавляет задачи выгрузок и отчётов.
func Register(reg *jobs.Registry, svc *Service, log *slog.Logger) {
	jobs.Register(reg, &ExportWorker{Service: svc})
	jobs.Register(reg, &RenderWorker{Service: svc})
	jobs.Register(reg, &ScheduleWorker{Service: svc, Log: log})
	jobs.Register(reg, &CleanupWorker{Service: svc, Log: log})
	reg.Every(CleanupArgs{}.Kind(), time.Hour, CleanupArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}, false)
	reg.Every(ScheduleArgs{}.Kind(), 5*time.Minute, ScheduleArgs{}, &river.InsertOpts{Queue: jobs.QueueReports}, true)
}

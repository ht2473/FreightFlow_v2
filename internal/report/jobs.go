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

// Register добавляет задачи выгрузок.
func Register(reg *jobs.Registry, svc *Service, log *slog.Logger) {
	jobs.Register(reg, &ExportWorker{Service: svc})
	jobs.Register(reg, &CleanupWorker{Service: svc, Log: log})
	reg.Every(CleanupArgs{}.Kind(), time.Hour, CleanupArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}, false)
}

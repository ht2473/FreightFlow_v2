package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// RunArgs — задача выполнения запуска.
type RunArgs struct {
	RunID uuid.UUID `json:"run_id"`
}

// Kind — вид задачи.
func (RunArgs) Kind() string { return "ingest.run" }

// InsertOpts — один запуск не исполняется дважды; повтор только при сбое записи состояния.
func (RunArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueIngest, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// RunWorker выполняет запуски загрузок.
type RunWorker struct {
	river.WorkerDefaults[RunArgs]
	Service *Service
}

// Work выполняет запуск.
func (w *RunWorker) Work(ctx context.Context, job *river.Job[RunArgs]) error {
	return w.Service.Execute(ctx, job.Args.RunID)
}

// Timeout — предел задачи; сам запуск ограничен описанием источника.
func (w *RunWorker) Timeout(*river.Job[RunArgs]) time.Duration { return 12 * time.Hour }

// ScheduleArgs — плановый запуск источника.
type ScheduleArgs struct {
	Source string `json:"source"`
}

// Kind — вид задачи.
func (ScheduleArgs) Kind() string { return "ingest.schedule" }

// ScheduleWorker ставит плановый запуск, если источник включён и не загружается сейчас.
type ScheduleWorker struct {
	river.WorkerDefaults[ScheduleArgs]
	Service *Service
}

// Work ставит запуск в очередь.
func (w *ScheduleWorker) Work(ctx context.Context, job *river.Job[ScheduleArgs]) error {
	st, err := w.Service.Source(ctx, job.Args.Source)
	if errors.Is(err, ErrUnknownSource) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Enabled {
		return nil
	}
	_, err = w.Service.Enqueue(ctx, job.Args.Source, TriggerSchedule, false, nil)
	if errors.Is(err, ErrRunActive) {
		return nil
	}
	return err
}

// CleanupArgs — завершение зависших запусков.
type CleanupArgs struct{}

// Kind — вид задачи.
func (CleanupArgs) Kind() string { return "ingest.cleanup" }

// CleanupWorker завершает запуски, прерванные остановкой процесса.
type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	Service *Service
}

// Work завершает зависшие запуски.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	_, err := w.Service.FailStaleRuns(ctx)
	return err
}

// Register добавляет задачи загрузок и расписания источников в реестр.
func Register(reg *jobs.Registry, svc *Service) {
	jobs.Register(reg, &RunWorker{Service: svc})
	jobs.Register(reg, &ScheduleWorker{Service: svc})
	jobs.Register(reg, &CleanupWorker{Service: svc})
	reg.Every("ingest.cleanup", time.Hour, CleanupArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}, true)
	for _, d := range svc.Descriptors() {
		if len(d.Tools) > 0 {
			queue := d.Queue
			if queue == "" {
				queue = jobs.QueueIngest
			}
			reg.RequireTools(queue, d.Tools...)
		}
		if d.Interval == 0 {
			continue
		}
		reg.Every("ingest.schedule."+d.Code, d.Interval, ScheduleArgs{Source: d.Code},
			&river.InsertOpts{Queue: jobs.QueueMaintenance}, false)
	}
}

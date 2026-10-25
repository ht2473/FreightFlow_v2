package search

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// ReindexArgs — переиндексация видов, данные которых изменились.
type ReindexArgs struct{}

// Kind — вид задачи.
func (ReindexArgs) Kind() string { return "search.reindex" }

// ReindexWorker сверяет подписи данных и перестраивает изменившиеся виды документов.
type ReindexWorker struct {
	river.WorkerDefaults[ReindexArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — полное построение адресов идёт несколько минут.
func (w *ReindexWorker) Timeout(*river.Job[ReindexArgs]) time.Duration { return 30 * time.Minute }

// Work перестраивает изменившиеся виды.
func (w *ReindexWorker) Work(ctx context.Context, _ *river.Job[ReindexArgs]) error {
	res, err := w.Service.Reindex(ctx, nil, false)
	for _, r := range res {
		if !r.Skipped {
			w.Log.InfoContext(ctx, "search reindexed", "kind", r.Kind, "documents", r.Documents, "duration", r.Duration)
		}
	}
	return err
}

// logRetention — срок хранения журнала запросов.
const logRetention = 90 * 24 * time.Hour

// LogCleanupArgs — удаление старых записей журнала запросов.
type LogCleanupArgs struct{}

// Kind — вид задачи.
func (LogCleanupArgs) Kind() string { return "search.log_cleanup" }

// LogCleanupWorker удаляет записи журнала запросов старше 90 дней.
type LogCleanupWorker struct {
	river.WorkerDefaults[LogCleanupArgs]
	Service *Service
}

// Work удаляет старые записи.
func (w *LogCleanupWorker) Work(ctx context.Context, _ *river.Job[LogCleanupArgs]) error {
	_, err := w.Service.pool.Exec(ctx, `DELETE FROM search.query_log WHERE at < $1`, time.Now().Add(-logRetention))
	return err
}

// Register добавляет переиндексацию (при запуске и каждые две минуты: проверка подписей
// дешёвая) и ежесуточную очистку журнала запросов.
func Register(reg *jobs.Registry, svc *Service, log *slog.Logger) {
	jobs.Register(reg, &ReindexWorker{Service: svc, Log: log})
	jobs.Register(reg, &LogCleanupWorker{Service: svc})
	opts := &river.InsertOpts{Queue: jobs.QueueSearch, UniqueOpts: river.UniqueOpts{ByArgs: true}}
	reg.Every(ReindexArgs{}.Kind(), 2*time.Minute, ReindexArgs{}, opts, true)
	reg.Every(LogCleanupArgs{}.Kind(), 24*time.Hour, LogCleanupArgs{}, opts, false)
}

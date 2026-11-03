package alerting

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/events"
	"freightflow/internal/platform/jobs"
)

// CheckArgs — проверка всех включённых правил.
type CheckArgs struct{}

// Kind — вид задачи.
func (CheckArgs) Kind() string { return "alerting.check" }

// CheckWorker проверяет правила. Проверки идут без уникальности: каждая идемпотентна
// (срабатывания записаны), а одновременные проверки правила сериализует блокировка строки.
type CheckWorker struct {
	river.WorkerDefaults[CheckArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — проверка правил с большими областями ограничений занимает до минуты.
func (w *CheckWorker) Timeout(*river.Job[CheckArgs]) time.Duration { return 10 * time.Minute }

// Work проверяет правила.
func (w *CheckWorker) Work(ctx context.Context, _ *river.Job[CheckArgs]) error {
	start := time.Now()
	n, err := w.Service.CheckAll(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		w.Log.InfoContext(ctx, "alert rules checked", "hits", n, "duration", time.Since(start))
	}
	return nil
}

// checkInterval — проверка по расписанию: устаревание источников и изменения без событий.
const checkInterval = 15 * time.Minute

// Register добавляет проверку правил и её расписание.
func Register(reg *jobs.Registry, svc *Service, log *slog.Logger) {
	jobs.Register(reg, &CheckWorker{Service: svc, Log: log})
	reg.Every(CheckArgs{}.Kind(), checkInterval, CheckArgs{}, &river.InsertOpts{Queue: jobs.QueueAlerts}, true)
}

// triggers — события, после которых правила проверяются сразу: изменения работ, публикации
// данных (ограничения, доступность, реестр) и завершение загрузок.
var triggers = []string{events.RoadworkChanged, events.RestrictionChanged, events.DataPublished, events.IngestRunFinished}

// Consumer — постоянный потребитель событий: порция событий ставит одну проверку правил в той
// же транзакции, в которой сдвигается позиция, поэтому ни одно изменение не остаётся без проверки.
func (s *Service) Consumer() events.Consumer {
	return events.Consumer{
		Name:  "alerting.check",
		Types: triggers,
		Handle: func(ctx context.Context, tx pgx.Tx, _ []events.Event) error {
			return s.enqueueCheck(ctx, tx)
		},
	}
}

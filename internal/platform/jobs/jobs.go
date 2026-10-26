// Package jobs — фоновые задачи на очереди River в PostgreSQL: реестр обработчиков,
// периодические задачи, клиенты для постановки и исполнения.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"freightflow/internal/platform/migrate"
)

// Очереди задач (03-architecture.md §17). Параллелизм ограничен по очереди: тяжёлые
// импорты и расчёты не вытесняют доставку уведомлений.
const (
	QueueIngest      = "ingest"
	QueueOSM         = "osm"
	QueueRouting     = "routing"
	QueueScenario    = "scenario"
	QueueAnalytics   = "analytics"
	QueueSearch      = "search"
	QueueReports     = "reports"
	QueueNotify      = "notify"
	QueueAlerts      = "alerts"
	QueueMaintenance = "maintenance"
)

var queueWorkers = map[string]int{
	river.QueueDefault: 2,
	QueueIngest:        2,
	QueueOSM:           1,
	QueueRouting:       1,
	// Сценарии пользователя — отдельно от часового сетевого анализа в routing.
	QueueScenario:    2,
	QueueAnalytics:   2,
	QueueSearch:      1,
	QueueReports:     2,
	QueueNotify:      4,
	QueueAlerts:      2,
	QueueMaintenance: 1,
}

// Client — клиент очереди; Insert и InsertTx ставят задачи.
type Client = river.Client[pgx.Tx]

// Args — аргументы задачи: вид (Kind) и данные, сериализуемые в JSON.
type Args = river.JobArgs

// Registry собирает обработчики и периодические задачи модулей.
type Registry struct {
	workers  *river.Workers
	periodic []*river.PeriodicJob
	tools    map[string][]string
}

// NewRegistry создаёт пустой реестр.
func NewRegistry() *Registry {
	return &Registry{workers: river.NewWorkers(), tools: map[string][]string{}}
}

// RequireTools отмечает, что задачам очереди нужны внешние программы. Процесс, в котором
// их нет (например, сервер на Windows), такую очередь не обслуживает — её задачи
// выполнит worker в контейнере с инструментами.
func (r *Registry) RequireTools(queue string, tools ...string) {
	r.tools[queue] = append(r.tools[queue], tools...)
}

// missingTool возвращает первую недоступную программу очереди.
func (r *Registry) missingTool(queue string) string {
	for _, t := range r.tools[queue] {
		if _, err := exec.LookPath(t); err != nil {
			return t
		}
	}
	return ""
}

// Register добавляет обработчик задач одного вида.
func Register[T river.JobArgs](r *Registry, w river.Worker[T]) {
	river.AddWorker(r.workers, w)
}

// Every ставит задачу по расписанию с заданным интервалом. id различает периодические
// задачи одного вида (например, расписания разных источников). Запуск при старте нужен
// задачам, которые важно выполнить сразу после развёртывания.
func (r *Registry) Every(id string, interval time.Duration, args Args, opts *river.InsertOpts, runOnStart bool) {
	r.periodic = append(r.periodic, river.NewPeriodicJob(
		river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) { return args, opts },
		&river.PeriodicJobOpts{ID: id, RunOnStart: runOnStart},
	))
}

// Observability — провайдеры метрик и трасс для задач.
type Observability struct {
	Meters  metric.MeterProvider
	Tracers trace.TracerProvider
}

// NewInserter создаёт клиент, который только ставит задачи (веб-сервер).
func NewInserter(pool *pgxpool.Pool, log *slog.Logger) (*Client, error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema: migrate.QueueSchema,
		Logger: log.With("component", "jobs"),
	})
	if err != nil {
		return nil, fmt.Errorf("create job inserter: %w", err)
	}
	return c, nil
}

// NewWorker создаёт клиент, который исполняет задачи всех очередей и ставит периодические.
// Запуск — Start, остановка — Stop (дожидается текущих задач).
func NewWorker(pool *pgxpool.Pool, log *slog.Logger, reg *Registry, obs Observability) (*Client, error) {
	l := log.With("component", "jobs")
	queues := make(map[string]river.QueueConfig, len(queueWorkers))
	for q, n := range queueWorkers {
		if tool := reg.missingTool(q); tool != "" {
			l.Info("queue skipped: required tool not found", "queue", q, "tool", tool)
			continue
		}
		queues[q] = river.QueueConfig{MaxWorkers: n}
	}
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:       migrate.QueueSchema,
		Logger:       l,
		Queues:       queues,
		Workers:      reg.workers,
		PeriodicJobs: reg.periodic,
		JobTimeout:   time.Hour,
		MaxAttempts:  10,
		ErrorHandler: &errorHandler{log: l},
		Middleware: []rivertype.Middleware{otelriver.NewMiddleware(&otelriver.MiddlewareConfig{
			MeterProvider:  obs.Meters,
			TracerProvider: obs.Tracers,
		})},
	})
	if err != nil {
		return nil, fmt.Errorf("create job worker: %w", err)
	}
	return c, nil
}

// errorHandler пишет сбои задач в журнал; повторы выполняет River по своей политике.
type errorHandler struct{ log *slog.Logger }

func (h *errorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	h.log.WarnContext(ctx, "job failed", "kind", job.Kind, "id", job.ID, "attempt", job.Attempt, "error", err)
	return nil
}

func (h *errorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, v any, trace string) *river.ErrorHandlerResult {
	h.log.ErrorContext(ctx, "job panic", "kind", job.Kind, "id", job.ID, "attempt", job.Attempt, "panic", v, "stack", trace)
	return nil
}

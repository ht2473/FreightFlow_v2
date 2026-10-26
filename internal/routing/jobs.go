package routing

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

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

// AnalysisArgs — сетевой анализ, если дорожная сеть новее графа анализа.
type AnalysisArgs struct{}

// Kind — вид задачи.
func (AnalysisArgs) Kind() string { return "routing.network_analysis" }

// AnalysisWorker строит граф анализа и пересчитывает значимость рёбер и узкие места после
// публикации новой версии дорожной сети.
type AnalysisWorker struct {
	river.WorkerDefaults[AnalysisArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — две оценки центральности по сети Москвы.
func (w *AnalysisWorker) Timeout(*river.Job[AnalysisArgs]) time.Duration { return 2 * time.Hour }

// Work сравнивает версию домена network с версией, по которой построен текущий граф.
func (w *AnalysisWorker) Work(ctx context.Context, _ *river.Job[AnalysisArgs]) error {
	var stale bool
	if err := w.Service.pool.QueryRow(ctx, `
		SELECT coalesce((SELECT version FROM ingest.data_version WHERE domain = 'network'), 0)
		       IS DISTINCT FROM (SELECT network_version FROM network.graph_version WHERE id = network.current_graph())`).
		Scan(&stale); err != nil {
		return err
	}
	if !stale {
		return nil
	}
	st, err := w.Service.AnalyzeNetwork(ctx, DefaultCentrality, nil)
	if err != nil {
		return err
	}
	w.Log.InfoContext(ctx, "network analysis built", "version", st.Version, "nodes", st.Nodes, "edges", st.Edges,
		"sources", st.Profile.Sources, "bottlenecks", st.Bottlenecks, "duration", st.Duration)
	return nil
}

// Register добавляет ежечасные проверки: граф маршрутизации — пересчёт доступности объектов и
// ячеек H3, дорожная сеть — сетевой анализ.
func Register(reg *jobs.Registry, pool *pgxpool.Pool, valhallaURL string, log *slog.Logger) {
	svc := NewService(pool, NewClient(valhallaURL))
	jobs.Register(reg, &AccessibilityWorker{Service: svc, Log: log})
	jobs.Register(reg, &AnalysisWorker{Service: svc, Log: log})
	jobs.Register(reg, &CellAccessWorker{Service: svc, Log: log})
	opts := &river.InsertOpts{Queue: jobs.QueueRouting, UniqueOpts: river.UniqueOpts{ByArgs: true}}
	reg.Every(AccessibilityArgs{}.Kind(), time.Hour, AccessibilityArgs{}, opts, false)
	reg.Every(AnalysisArgs{}.Kind(), time.Hour, AnalysisArgs{}, opts, false)
	// Доступность ячеек — в очереди сценариев: сетевой анализ в routing идёт до двух часов.
	// Уникальность — только среди стоящих в очереди и выполняющихся: завершённая проверка не
	// мешает следующей.
	active := []rivertype.JobState{
		rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
		rivertype.JobStateRunning, rivertype.JobStateScheduled,
	}
	reg.Every(CellAccessArgs{}.Kind(), time.Hour, CellAccessArgs{},
		&river.InsertOpts{Queue: jobs.QueueScenario, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: active}}, true)
}

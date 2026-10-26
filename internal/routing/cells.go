package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
)

// Доступность ячеек H3 — критерий оценки локации: время грузовика от центра ячейки разрешения 8
// до ближайшей цели вида. Ячейки разрешения 9 берут значение родителя (показатели аналитики).

// cellProfile — профиль ТС доступности ячеек, как в сетевом анализе.
const cellProfile = "truck_12"

// cellTargets — виды целей доступности ячеек.
var cellTargets = []string{"mkad", "ttk", "rail_freight"}

// cellWorkers — параллельных запросов матриц.
const cellWorkers = 4

// CellAccessStats — итог расчёта доступности ячеек.
type CellAccessStats struct {
	Cells, Requests, Unreachable int
	Skipped                      bool
	GraphBuiltAt                 time.Time
	Duration                     time.Duration
}

// RefreshCellAccess пересчитывает доступность ячеек, если сменились граф маршрутизации или набор
// ячеек (или force).
func (s *Service) RefreshCellAccess(ctx context.Context, force bool) (CellAccessStats, error) {
	start := time.Now()
	var st CellAccessStats
	status, err := s.vh.Status(ctx)
	if err != nil {
		return st, err
	}
	st.GraphBuiltAt = time.Unix(status.Tileset, 0)
	rows, err := s.pool.Query(ctx, `SELECT h3::text, ST_X(h3_cell_to_geometry(h3)), ST_Y(h3_cell_to_geometry(h3))
		FROM territory.h3_cell WHERE res = 8 ORDER BY h3`)
	if err != nil {
		return st, err
	}
	type cell struct {
		h3 string
		pt Point
	}
	cells, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cell, error) {
		var c cell
		return c, r.Scan(&c.h3, &c.pt.Lon, &c.pt.Lat)
	})
	if err != nil {
		return st, err
	}
	st.Cells = len(cells)
	if len(cells) == 0 {
		st.Skipped = true
		return st, nil
	}
	if !force {
		var stored int
		var built *time.Time
		if err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT c.h3)::int, min(a.graph_built_at)
			FROM analytics.cell_access a JOIN territory.h3_cell c ON c.h3 = a.h3`).Scan(&stored, &built); err != nil {
			return st, err
		}
		if stored == len(cells) && built != nil && built.Equal(st.GraphBuiltAt) {
			st.Skipped = true
			return st, nil
		}
	}
	prof, err := s.profile(ctx, cellProfile)
	if err != nil {
		return st, err
	}
	targets, err := s.Targets(ctx)
	if err != nil {
		return st, err
	}
	points := make([]Point, len(cells))
	for i, c := range cells {
		points[i] = c.pt
	}
	var out [][]any
	for _, kind := range cellTargets {
		ts := targets[kind]
		if len(ts) == 0 {
			continue
		}
		minutes, km, err := s.Nearest(ctx, NearestInput{Sources: points, Targets: ts, Profile: prof, Workers: cellWorkers})
		if err != nil {
			return st, fmt.Errorf("%s: %w", kind, err)
		}
		st.Requests += batches(len(points), len(ts))
		for i, c := range cells {
			var m, k *float64
			if minutes[i] >= 0 {
				m, k = &minutes[i], &km[i]
			} else {
				st.Unreachable++
			}
			out = append(out, []any{c.h3, kind, m, k, st.GraphBuiltAt})
		}
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM analytics.cell_access`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE cell_access_in (h3 text, target_kind text, minutes real, km real,
			graph_built_at timestamptz) ON COMMIT DROP`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"cell_access_in"},
			[]string{"h3", "target_kind", "minutes", "km", "graph_built_at"}, pgx.CopyFromRows(out)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO analytics.cell_access (h3, target_kind, minutes, km, graph_built_at)
			SELECT h3::h3index, target_kind, minutes, km, graph_built_at FROM cell_access_in`); err != nil {
			return err
		}
		var v int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO ingest.data_version (domain, version, published_at) VALUES ('cell_access', 1, now())
			ON CONFLICT (domain) DO UPDATE SET version = ingest.data_version.version + 1, published_at = now()
			RETURNING version`).Scan(&v); err != nil {
			return err
		}
		return events.Publish(ctx, tx, events.DataPublished, map[string]any{"versions": map[string]int64{"cell_access": v}})
	})
	st.Duration = time.Since(start)
	return st, err
}

// batches — запросов матрицы на n источников и targets целей (порции как в Nearest).
func batches(n, targets int) int {
	b := min(maxSources, max(1, maxPairs/targets))
	return (n + b - 1) / b
}

// CellAccessArgs — проверка и пересчёт доступности ячеек H3.
type CellAccessArgs struct{}

// Kind — вид задачи.
func (CellAccessArgs) Kind() string { return "routing.cell_access" }

// CellAccessWorker пересчитывает доступность ячеек при новом графе.
type CellAccessWorker struct {
	river.WorkerDefaults[CellAccessArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — около трёхсот запросов матриц.
func (w *CellAccessWorker) Timeout(*river.Job[CellAccessArgs]) time.Duration { return time.Hour }

// Work пересчитывает доступность, если граф или ячейки сменились; без службы — до следующей проверки.
func (w *CellAccessWorker) Work(ctx context.Context, _ *river.Job[CellAccessArgs]) error {
	st, err := w.Service.RefreshCellAccess(ctx, false)
	if errors.Is(err, ErrUnavailable) {
		return nil
	}
	if err == nil && !st.Skipped {
		w.Log.InfoContext(ctx, "cell access refreshed", "cells", st.Cells, "requests", st.Requests,
			"unreachable", st.Unreachable, "duration", st.Duration)
	}
	return err
}

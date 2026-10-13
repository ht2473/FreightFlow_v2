package routing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
)

// maxPairs — пар «источник × цель» в одном запросе матрицы (предел службы — 2500).
const maxPairs = 2400

// maxSources — объектов в одном запросе матрицы.
const maxSources = 50

// facilityRadius — радиус привязки объекта к сети, м: подъезд к объекту ищется в его окрестности.
const facilityRadius = 300

type target struct {
	ID    int
	Point Point
}

type facilityPoint struct {
	ID    uuid.UUID
	Point Point
}

// AccessibilityStats — итог расчёта доступности.
type AccessibilityStats struct {
	Facilities, Targets, Profiles, Requests, Unreachable int
	GraphBuiltAt                                         time.Time
}

// RefreshAccessibility пересчитывает время от каждого объекта реестра до ближайшей цели каждого
// вида (МКАД, ТТК, центр, ж/д грузовые пункты) для всех типовых профилей. Зоны пропусков не
// исключаются: это физическая доступность по сети с ограничениями участков для профиля.
func (s *Service) RefreshAccessibility(ctx context.Context, progress func(string)) (AccessibilityStats, error) {
	var st AccessibilityStats
	status, err := s.vh.Status(ctx)
	if err != nil {
		return st, err
	}
	st.GraphBuiltAt = time.Unix(status.Tileset, 0)
	if _, err := s.pool.Exec(ctx, `SELECT analytics.refresh_accessibility_targets()`); err != nil {
		return st, fmt.Errorf("targets: %w", err)
	}
	targets, err := s.targets(ctx)
	if err != nil {
		return st, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, ST_X(point), ST_Y(point) FROM infra.facility WHERE status = 'active' ORDER BY id`)
	if err != nil {
		return st, err
	}
	facilities, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (facilityPoint, error) {
		var f facilityPoint
		return f, r.Scan(&f.ID, &f.Point.Lon, &f.Point.Lat)
	})
	if err != nil {
		return st, err
	}
	profiles, err := s.Profiles(ctx)
	if err != nil {
		return st, err
	}
	st.Facilities, st.Profiles = len(facilities), len(profiles)
	for _, ts := range targets {
		st.Targets += len(ts)
	}
	for _, p := range profiles {
		for kind, ts := range targets {
			if len(ts) == 0 {
				continue
			}
			n, unreachable, err := s.accessibilityFor(ctx, p, kind, ts, facilities, st.GraphBuiltAt)
			st.Requests += n
			st.Unreachable += unreachable
			if err != nil {
				return st, fmt.Errorf("%s, %s: %w", p.Code, kind, err)
			}
			if progress != nil {
				progress(fmt.Sprintf("%s: %s — готово, недостижимо %d", p.Name, kind, unreachable))
			}
		}
	}
	return st, publishAccessibility(ctx, s.pool)
}

// publishAccessibility повышает версию домена accessibility (тайлы объектов показывают время
// доступности) и сообщает о публикации.
func publishAccessibility(ctx context.Context, pool *pgxpool.Pool) error {
	return db.InTx(ctx, pool, func(tx pgx.Tx) error {
		var v int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO ingest.data_version (domain, version, published_at) VALUES ('accessibility', 1, now())
			ON CONFLICT (domain) DO UPDATE SET version = ingest.data_version.version + 1, published_at = now()
			RETURNING version`).Scan(&v); err != nil {
			return err
		}
		return events.Publish(ctx, tx, events.DataPublished, map[string]any{"versions": map[string]int64{"accessibility": v}})
	})
}

func (s *Service) targets(ctx context.Context) (map[string][]target, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, ST_X(point), ST_Y(point) FROM analytics.accessibility_target ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]target{}
	for rows.Next() {
		var t target
		var kind string
		if err := rows.Scan(&t.ID, &kind, &t.Point.Lon, &t.Point.Lat); err != nil {
			return nil, err
		}
		out[kind] = append(out[kind], t)
	}
	return out, rows.Err()
}

// accessibilityFor считает матрицы порциями объектов и сохраняет ближайшую цель каждого объекта.
func (s *Service) accessibilityFor(ctx context.Context, p Profile, kind string, ts []target, fs []facilityPoint,
	built time.Time,
) (requests, unreachable int, err error) {
	batch := min(maxSources, max(1, maxPairs/len(ts)))
	targetPoints := make([]Point, len(ts))
	for i, t := range ts {
		targetPoints[i] = t.Point
	}
	type best struct {
		minutes, km *float64
		target      *int
	}
	results := make([]best, len(fs))
	for start := 0; start < len(fs); start += batch {
		end := min(start+batch, len(fs))
		sources := make([]Point, end-start)
		for i := range sources {
			sources[i] = fs[start+i].Point
		}
		m, err := s.vh.Matrix(ctx, MatrixRequest{
			Sources: sources, Targets: targetPoints, Costing: "truck", Truck: p.truck(), SourceRadius: facilityRadius,
		})
		requests++
		if err != nil {
			return requests, unreachable, err
		}
		for i, row := range m {
			for j, c := range row {
				if !c.Reachable {
					continue
				}
				mins, km, id := c.Time/60, c.Length, ts[j].ID
				if b := &results[start+i]; b.minutes == nil || mins < *b.minutes {
					b.minutes, b.km, b.target = &mins, &km, &id
				}
			}
		}
	}
	batchRows := make([][]any, len(fs))
	for i, f := range fs {
		r := results[i]
		if r.minutes == nil {
			unreachable++
		}
		batchRows[i] = []any{f.ID, p.Code, kind, r.minutes, r.km, r.target, built}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return requests, unreachable, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM analytics.accessibility WHERE profile_code = $1 AND target_kind = $2`, p.Code, kind); err != nil {
		return requests, unreachable, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"analytics", "accessibility"},
		[]string{"facility_id", "profile_code", "target_kind", "minutes", "km", "target_id", "graph_built_at"},
		pgx.CopyFromRows(batchRows)); err != nil {
		return requests, unreachable, err
	}
	return requests, unreachable, tx.Commit(ctx)
}

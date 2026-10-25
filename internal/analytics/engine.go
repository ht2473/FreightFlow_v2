package analytics

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
)

// Service — показатели.
type Service struct {
	pool *pgxpool.Pool
}

// New — сервис показателей.
func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Computed — итог пересчёта показателя.
type Computed struct {
	Code     string
	Rows     int
	Skipped  bool // входные данные не изменились
	Duration time.Duration
}

// SyncCatalog выгружает описания показателей в analytics.metric_def; показатели, которых нет в
// коде, удаляются вместе со значениями.
func (s *Service) SyncCatalog(ctx context.Context) error {
	cat := Catalog()
	codes := make([]string, len(cat))
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		for i, m := range cat {
			codes[i] = m.Code
			if _, err := tx.Exec(ctx, `
				INSERT INTO analytics.metric_def (code, title, unit, levels, formula, method, domains, depends, version, synced_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
				ON CONFLICT (code) DO UPDATE SET title = excluded.title, unit = excluded.unit, levels = excluded.levels,
				    formula = excluded.formula, method = excluded.method, domains = excluded.domains,
				    depends = excluded.depends, version = excluded.version, synced_at = excluded.synced_at`,
				m.Code, m.Title, m.Unit, m.Levels, m.Formula, m.Method, nonNil(m.Domains), nonNil(m.DependsOn), m.Version); err != nil {
				return fmt.Errorf("metric %s: %w", m.Code, err)
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM analytics.metric_def WHERE NOT (code = ANY($1))`, codes)
		return err
	})
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// signature — подпись входов показателя: версия описания, версии доменов, время расчёта
// показателей-зависимостей, у ежедневных — дата.
func (s *Service) signature(ctx context.Context, m Metric) (string, error) {
	var sig string
	err := s.pool.QueryRow(ctx, `
		SELECT $1::text
		    || '|' || coalesce((SELECT string_agg(domain || ':' || version, ',' ORDER BY domain)
		                        FROM ingest.data_version WHERE domain = ANY($2)), '')
		    || '|' || coalesce((SELECT string_agg(code || ':' || computed_at::text, ',' ORDER BY code)
		                        FROM analytics.metric_state WHERE code = ANY($3)), '')
		    || '|' || CASE WHEN $4 THEN current_date::text ELSE '' END`,
		strconv.Itoa(m.Version), nonNil(m.Domains), nonNil(m.DependsOn), m.Daily).Scan(&sig)
	return sig, err
}

// Recompute пересчитывает показатели codes (пусто — все) в порядке зависимостей, если их входы
// изменились; force — без проверки.
func (s *Service) Recompute(ctx context.Context, codes []string, force bool) ([]Computed, error) {
	if err := s.SyncCatalog(ctx); err != nil {
		return nil, err
	}
	if err := s.prepareCells(ctx, force); err != nil {
		return nil, fmt.Errorf("h3 cells: %w", err)
	}
	all, err := ordered(Catalog())
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, c := range codes {
		if _, ok := Find(c); !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknown, c)
		}
		want[c] = true
	}
	var out []Computed
	changed := false
	for _, m := range all {
		if len(want) > 0 && !want[m.Code] {
			continue
		}
		res, err := s.compute(ctx, m, force)
		if err != nil {
			return out, fmt.Errorf("metric %s: %w", m.Code, err)
		}
		changed = changed || !res.Skipped
		out = append(out, res)
	}
	if changed {
		return out, s.publish(ctx)
	}
	return out, nil
}

// publish повышает версию домена analytics: от неё зависят тайлы с показателями.
func (s *Service) publish(ctx context.Context) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var v int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO ingest.data_version (domain, version, published_at) VALUES ('analytics', 1, now())
			ON CONFLICT (domain) DO UPDATE SET version = ingest.data_version.version + 1, published_at = now()
			RETURNING version`).Scan(&v); err != nil {
			return err
		}
		return events.Publish(ctx, tx, events.DataPublished, map[string]any{"versions": map[string]int64{"analytics": v}})
	})
}

// prepareCells строит ячейки H3 разрешений 8 и 9 в границах Москвы при новой версии данных
// территорий: ячейка — там, где её центр; район — по центру ячейки.
func (s *Service) prepareCells(ctx context.Context, force bool) error {
	var sig string
	if err := s.pool.QueryRow(ctx, `SELECT coalesce(max(version), 0)::text FROM ingest.data_version WHERE domain = 'territory'`).
		Scan(&sig); err != nil {
		return err
	}
	if !force {
		var prev string
		err := s.pool.QueryRow(ctx, `SELECT signature FROM analytics.prepare_state WHERE step = 'h3_cells'`).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if prev == sig {
			return nil
		}
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM territory.h3_cell`); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO territory.h3_cell (h3, res, district_id, okrug_id, geom, area_km2)
			SELECT c.h3, c.res, d.id, d.parent_id, h3_cell_to_boundary_geometry(c.h3), h3_cell_area(c.h3, 'km^2')
			FROM (SELECT h3_polygon_to_cells(m.geom, r.res) AS h3, r.res
			      FROM territory.admin_area m, (VALUES (8), (9)) r (res)
			      WHERE m.kind = 'region' AND m.name = 'Москва') c
			LEFT JOIN LATERAL (
				SELECT d.id, d.parent_id FROM territory.admin_area d
				WHERE d.kind = 'district' AND ST_Intersects(d.geom, h3_cell_to_geometry(c.h3)) LIMIT 1
			) d ON true`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO analytics.prepare_state (step, signature, rows, prepared_at) VALUES ('h3_cells', $1, $2, now())
			ON CONFLICT (step) DO UPDATE SET signature = excluded.signature, rows = excluded.rows, prepared_at = excluded.prepared_at`,
			sig, tag.RowsAffected())
		return err
	})
}

func (s *Service) compute(ctx context.Context, m Metric, force bool) (Computed, error) {
	start := time.Now()
	res := Computed{Code: m.Code}
	sig, err := s.signature(ctx, m)
	if err != nil {
		return res, err
	}
	if !force {
		var prev string
		err := s.pool.QueryRow(ctx, `SELECT signature FROM analytics.metric_state WHERE code = $1`, m.Code).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
		if prev == sig {
			res.Skipped = true
			return res, nil
		}
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM analytics.metric_value WHERE metric_code = $1`, m.Code); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO analytics.metric_value (metric_code, level, entity, period, value, inputs, computed_at)
			SELECT $1, c.level, c.entity, c.period, c.value, coalesce(c.inputs, '{}'), now()
			FROM (`+m.Compute+`) c (level, entity, period, value, inputs)
			WHERE c.value IS NOT NULL AND c.value::text NOT IN ('NaN', 'Infinity', '-Infinity')`, m.Code)
		if err != nil {
			return err
		}
		res.Rows = int(tag.RowsAffected())
		res.Duration = time.Since(start)
		_, err = tx.Exec(ctx, `
			INSERT INTO analytics.metric_state (code, signature, rows, duration_ms, computed_at) VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (code) DO UPDATE SET signature = excluded.signature, rows = excluded.rows,
			    duration_ms = excluded.duration_ms, computed_at = excluded.computed_at`,
			m.Code, sig, res.Rows, res.Duration.Milliseconds())
		return err
	})
	return res, err
}

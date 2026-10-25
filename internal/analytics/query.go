package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Def — описание показателя с состоянием расчёта.
type Def struct {
	Metric
	ComputedAt *time.Time
	Rows       int
}

// Defs — каталог с временем последнего расчёта.
func (s *Service) Defs(ctx context.Context) ([]Def, error) {
	state := map[string]Def{}
	rows, err := s.pool.Query(ctx, `SELECT code, computed_at, rows FROM analytics.metric_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var d Def
		var at time.Time
		if err := rows.Scan(&code, &at, &d.Rows); err != nil {
			return nil, err
		}
		d.ComputedAt = &at
		state[code] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cat := Catalog()
	out := make([]Def, len(cat))
	for i, m := range cat {
		st := state[m.Code]
		out[i] = Def{Metric: m, ComputedAt: st.ComputedAt, Rows: st.Rows}
	}
	return out, nil
}

// Value — значение показателя: объект уровня с подписью, период, входы, место среди объектов уровня.
type Value struct {
	Code   string
	Level  string
	Entity string
	Label  string
	Period *time.Time
	Value  float64
	Inputs json.RawMessage
	// Rank — место по убыванию среди объектов уровня (текущие значения), Of — их число.
	Rank, Of int
}

// labels — подпись объекта уровня.
const labels = `CASE v.level
	WHEN 'city' THEN 'Москва'
	WHEN 'district' THEN (SELECT name FROM territory.admin_area WHERE id::text = v.entity)
	WHEN 'okrug' THEN (SELECT coalesce(short_name, name) FROM territory.admin_area WHERE id::text = v.entity)
	WHEN 'facility' THEN (SELECT coalesce(f.name, t.name) FROM infra.facility f JOIN ref.facility_type t ON t.code = f.type_code
	                      WHERE f.id::text = v.entity)
	WHEN 'segment' THEN coalesce(v.inputs ->> 'name', (SELECT s.name FROM network.road_segment s WHERE s.id::text = v.entity))
	ELSE v.entity END`

func scanValues(rows pgx.Rows) ([]Value, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Value, error) {
		var v Value
		var label *string
		err := r.Scan(&v.Code, &v.Level, &v.Entity, &label, &v.Period, &v.Value, &v.Inputs, &v.Rank, &v.Of)
		if label != nil {
			v.Label = *label
		}
		return v, err
	})
}

// Values — значения показателя на уровне: текущие (period пуст) по убыванию значения или ряд
// по периодам; не больше limit.
func (s *Service) Values(ctx context.Context, code, level string, series bool, limit int) ([]Value, error) {
	m, ok := Find(code)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknown, code)
	}
	if level == "" && len(m.Levels) > 0 {
		level = m.Levels[0]
	}
	if !slices.Contains(m.Levels, level) {
		return nil, fmt.Errorf("%w: уровень %s у %s", ErrUnknown, level, code)
	}
	order := `v.value DESC`
	cond := `v.period IS NULL`
	if series {
		order, cond = `v.period`, `v.period IS NOT NULL`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT v.metric_code, v.level, v.entity, `+labels+`, v.period, v.value, v.inputs,
		       (rank() OVER (ORDER BY v.value DESC))::int, (count(*) OVER ())::int
		FROM analytics.metric_value v
		WHERE v.metric_code = $1 AND v.level = $2 AND `+cond+`
		ORDER BY `+order+` LIMIT $3`, code, level, limit)
	if err != nil {
		return nil, err
	}
	return scanValues(rows)
}

// EntityValues — текущие значения всех показателей одного объекта уровня с местом среди
// объектов того же уровня.
func (s *Service) EntityValues(ctx context.Context, level, entity string) ([]Value, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT v.metric_code, v.level, v.entity, `+labels+`, v.period, v.value, v.inputs, v.rank, v.n FROM (
			SELECT m.metric_code, m.level, m.entity, m.period, m.value, m.inputs,
			       (rank() OVER (PARTITION BY m.metric_code ORDER BY m.value DESC))::int AS rank,
			       (count(*) OVER (PARTITION BY m.metric_code))::int AS n
			FROM analytics.metric_value m
			WHERE m.level = $1 AND m.period IS NULL
		) v WHERE v.entity = $2
		ORDER BY v.metric_code`, level, entity)
	if err != nil {
		return nil, err
	}
	return scanValues(rows)
}

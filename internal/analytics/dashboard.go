package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Widget — виджет дашборда: плитка показателя города, ряд с прогнозом, рейтинг показателя на
// уровне или рейтинг индекса.
type Widget struct {
	Kind   string `json:"kind"`
	Metric string `json:"metric,omitempty"`
	Index  string `json:"index,omitempty"`
	Level  string `json:"level,omitempty"`
}

// Виды виджетов.
const (
	WidgetKPI    = "kpi"
	WidgetSeries = "series"
	WidgetRating = "rating"
	WidgetIndex  = "index"
)

// MaxWidgets — виджетов на дашборде, не больше.
const MaxWidgets = 16

// ErrWidget — неверный виджет.
var ErrWidget = errors.New("analytics: invalid widget")

// DefaultWidgets — дашборд по умолчанию.
var DefaultWidgets = []Widget{
	{Kind: WidgetKPI, Metric: "territory.facilities"},
	{Kind: WidgetKPI, Metric: "territory.median_mkad_min"},
	{Kind: WidgetKPI, Metric: "territory.restricted_share"},
	{Kind: WidgetKPI, Metric: "territory.population_coverage"},
	{Kind: WidgetSeries, Metric: "city.passes_valid"},
	{Kind: WidgetSeries, Metric: "city.works_active"},
	{Kind: WidgetIndex, Index: "index.attractiveness", Level: LevelDistrict},
	{Kind: WidgetRating, Metric: "facility.works_500m", Level: LevelFacility},
}

func validWidget(w Widget) error {
	switch w.Kind {
	case WidgetKPI:
		if m, ok := Find(w.Metric); !ok || !slices.Contains(m.Levels, LevelCity) || m.Series != "" {
			return fmt.Errorf("%w: плитка %s — нужен показатель города без ряда", ErrWidget, w.Metric)
		}
	case WidgetSeries:
		if m, ok := Find(w.Metric); !ok || m.Series == "" {
			return fmt.Errorf("%w: ряд %s", ErrWidget, w.Metric)
		}
	case WidgetRating:
		if m, ok := Find(w.Metric); !ok || !slices.Contains(m.Levels, w.Level) || m.Series != "" {
			return fmt.Errorf("%w: рейтинг %s на уровне %s", ErrWidget, w.Metric, w.Level)
		}
	case WidgetIndex:
		if ix, ok := FindIndex(w.Index); !ok || ix.Levels[w.Level] == nil {
			return fmt.Errorf("%w: индекс %s на уровне %s", ErrWidget, w.Index, w.Level)
		}
	default:
		return fmt.Errorf("%w: вид %q", ErrWidget, w.Kind)
	}
	return nil
}

// Dashboard — виджеты пользователя; own — свой набор (иначе по умолчанию). Виджеты показателей,
// которых больше нет в каталоге, отбрасываются.
func (s *Service) Dashboard(ctx context.Context, user uuid.UUID) ([]Widget, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT widgets FROM analytics.dashboard WHERE owner_id = $1`, user).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultWidgets, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var ws []Widget
	if err := json.Unmarshal(raw, &ws); err != nil {
		return nil, false, err
	}
	return slices.DeleteFunc(ws, func(w Widget) bool { return validWidget(w) != nil }), true, nil
}

// SaveDashboard сохраняет виджеты пользователя; nil — вернуться к набору по умолчанию.
func (s *Service) SaveDashboard(ctx context.Context, user uuid.UUID, ws []Widget) error {
	if ws == nil {
		_, err := s.pool.Exec(ctx, `DELETE FROM analytics.dashboard WHERE owner_id = $1`, user)
		return err
	}
	if len(ws) > MaxWidgets {
		return fmt.Errorf("%w: больше %d виджетов", ErrWidget, MaxWidgets)
	}
	for _, w := range ws {
		if err := validWidget(w); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO analytics.dashboard (owner_id, widgets, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (owner_id) DO UPDATE SET widgets = excluded.widgets, updated_at = excluded.updated_at`, user, raw)
	return err
}

// Geo — значения показателя на уровне с геометрией (GeoJSON FeatureCollection): районы и
// округа — упрощённая граница, ячейки H3 — шестиугольник.
func (s *Service) Geo(ctx context.Context, code, level string) (json.RawMessage, error) {
	m, ok := Find(code)
	if !ok || !slices.Contains(m.Levels, level) || m.Series != "" {
		return nil, fmt.Errorf("%w: %s на уровне %s", ErrUnknown, code, level)
	}
	var geom string
	switch level {
	case LevelDistrict, LevelOkrug:
		geom = `(SELECT ST_SimplifyPreserveTopology(a.geom, 0.0003) FROM territory.admin_area a WHERE a.id::text = v.entity)`
	case LevelH3r8, LevelH3r9:
		geom = `(SELECT c.geom FROM territory.h3_cell c WHERE c.h3::text = v.entity)`
	default:
		return nil, fmt.Errorf("%w: карта уровня %s", ErrUnknown, level)
	}
	var out []byte
	err := s.pool.QueryRow(ctx, `
		SELECT json_build_object('type', 'FeatureCollection', 'features', coalesce(json_agg(json_build_object(
			'type', 'Feature', 'id', v.entity,
			'properties', json_build_object('entity', v.entity, 'value', v.value, 'label', `+labels+`),
			'geometry', ST_AsGeoJSON(`+geom+`, 5)::json)), '[]'::json))
		FROM analytics.metric_value v
		WHERE v.metric_code = $1 AND v.level = $2 AND v.period IS NULL`, code, level).Scan(&out)
	return out, err
}

package facility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/paging"
)

// Service — выборки реестра объектов.
type Service struct {
	pool *pgxpool.Pool
}

// New создаёт сервис.
func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Пределы страницы списка.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// cursor — позиция страницы: смещение при том же фильтре и сортировке.
type cursor struct {
	Offset int `json:"o"`
}

const itemColumns = `
	f.id, f.name, f.type_code, t.name, coalesce(f.official_address, f.address), coalesce(o.short_name, o.name), d.name, f.ring,
	f.building_area_m2, f.floor_area_m2, f.territory_area_m2, f.buildings, f.confidence, acc.minutes::float8,
	` + hasPermit + `, ST_X(f.point), ST_Y(f.point), f.first_seen_at`

func scanItem(r pgx.Row, it *Item, extra ...any) error {
	var conf float32
	dest := []any{
		&it.ID, &it.Name, &it.Type, &it.TypeName, &it.Address, &it.Okrug, &it.District, &it.Ring,
		&it.BuildingM2, &it.FloorM2, &it.TerritoryM2, &it.Buildings, &conf, &it.MkadMin,
		&it.HasPermit, &it.Lon, &it.Lat, &it.FirstSeen,
	}
	if err := r.Scan(append(dest, extra...)...); err != nil {
		return err
	}
	it.Confidence = float64(conf)
	return nil
}

// List возвращает страницу реестра.
func (s *Service) List(ctx context.Context, f Filter, sort, after string, limit int) (Page, error) {
	var page Page
	order, err := orderBy(sort)
	if err != nil {
		return page, err
	}
	var pos cursor
	if _, err := paging.Decode(after, &pos); err != nil || pos.Offset < 0 {
		return page, fmt.Errorf("%w: cursor", ErrBadFilter)
	}
	limit = paging.Limit(limit, DefaultLimit, MaxLimit)
	w, err := f.conditions("")
	if err != nil {
		return page, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+itemColumns+`, count(*) OVER ()`+fromClause+`
		WHERE `+w.sql()+` ORDER BY `+order+fmt.Sprintf(` LIMIT %d OFFSET %d`, limit+1, pos.Offset), w.args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it, &page.Total); err != nil {
			return page, err
		}
		page.Items = append(page.Items, it)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		if page.Next, err = paging.Encode(cursor{Offset: pos.Offset + limit}); err != nil {
			return page, err
		}
	}
	if page.Items == nil {
		page.Items = []Item{}
	}
	return page, nil
}

// Facets считает распределения выборки по типу, округу, кольцу и достоверности.
func (s *Service) Facets(ctx context.Context, f Filter) (Facets, error) {
	var out Facets
	for _, fc := range []struct {
		dim, value, label, group, order string
		dst                             *[]FacetValue
	}{
		{"type", "f.type_code", "t.name", "f.type_code, t.name, t.ord", "t.ord", &out.Types},
		{"okrug", "o.id::text", "replace(o.name, ' административный округ', '')", "o.id, o.name", "count(*) DESC, o.name", &out.Okrugs},
		{"ring", "f.ring", "f.ring", "f.ring", "array_position(ARRAY['sk', 'ttk', 'mkad', 'ckad', 'outside'], f.ring)", &out.Rings},
		{
			"confidence", confidenceBucket, confidenceBucket, confidenceBucket,
			"min(f.confidence) DESC", &out.Confidence,
		},
	} {
		w, err := f.conditions(fc.dim)
		if err != nil {
			return out, err
		}
		rows, err := s.pool.Query(ctx, `SELECT `+fc.value+`, `+fc.label+`, count(*)`+fromClause+`
			WHERE `+w.sql()+` AND `+fc.value+` IS NOT NULL GROUP BY `+fc.group+` ORDER BY `+fc.order, w.args...)
		if err != nil {
			return out, fmt.Errorf("facet %s: %w", fc.dim, err)
		}
		*fc.dst, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FacetValue, error) {
			var v FacetValue
			return v, r.Scan(&v.Value, &v.Label, &v.Count)
		})
		if err != nil {
			return out, err
		}
	}
	for i, v := range out.Confidence {
		out.Confidence[i].Label = confidenceLabels[v.Value]
	}
	for i, v := range out.Rings {
		out.Rings[i].Label = ringLabels[v.Value]
	}
	return out, nil
}

// Уровни достоверности записи.
const confidenceBucket = `CASE WHEN f.confidence >= 0.7 THEN 'high' WHEN f.confidence >= 0.4 THEN 'medium' ELSE 'low' END`

var confidenceLabels = map[string]string{"high": "высокая (от 0,7)", "medium": "средняя (0,4–0,7)", "low": "низкая (до 0,4)"}

var ringLabels = map[string]string{
	"sk": "внутри Садового кольца", "ttk": "Садовое — ТТК", "mkad": "ТТК — МКАД", "ckad": "МКАД — ЦКАД", "outside": "за ЦКАД",
}

// Card — карточка объекта: строка реестра, геометрия, источники, достоверность, доступность,
// ограничения, работы и инфраструктура рядом.
type Card struct {
	Item
	Status            string
	Footprint         json.RawMessage
	Territory         json.RawMessage
	Levels            *int
	OSMType           string
	OSMID             int64
	Attrs             json.RawMessage
	ConfidenceReasons json.RawMessage
	LastSeen          time.Time
	UpdatedAt         time.Time
	// Curated — поля, заданные редактором.
	Curated      []string
	Sources      []Source
	Access       []Access
	Restrictions []Restriction
	Works        []Work
	Nearby       []Nearby
}

// Source — связь объекта с официальной записью (адрес реестра, разрешение на ввод).
type Source struct {
	Relation string
	Ref      string
	Details  json.RawMessage
}

// Access — время до ближайшей цели вида для профиля ТС.
type Access struct {
	Profile, ProfileName, Target string
	TargetName                   *string
	Minutes, Km                  *float64
}

// Restriction — ограничение на участке сети в 500 м от объекта.
type Restriction struct {
	Kind      string
	Value     *float64
	Condition *string
	Street    *string
	Origins   []string
	DistanceM float64
}

// Work — дорожные работы рядом с объектом.
type Work struct {
	ID        uuid.UUID
	Title     string
	Kind      string
	State     string
	Closure   bool
	Starts    *time.Time
	Ends      *time.Time
	DistanceM float64
}

// Nearby — ближайшая точка инфраструктуры вида в 3 км.
type Nearby struct {
	Kind      string
	Name      *string
	DistanceM float64
	Lon, Lat  float64
}

// Get возвращает карточку объекта (любого статуса).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Card, error) {
	var c Card
	var levels *int16
	row := s.pool.QueryRow(ctx, `SELECT `+itemColumns+`, f.status, ST_AsGeoJSON(f.footprint, 6), ST_AsGeoJSON(f.territory, 6),
		f.levels, f.osm_type, f.osm_id, f.attrs, f.confidence_reasons, f.last_seen_at, f.updated_at, f.curated`+fromClause+`
		WHERE f.id = $1`, id)
	var footprint, territory *string
	err := scanItem(row, &c.Item, &c.Status, &footprint, &territory, &levels, &c.OSMType, &c.OSMID, &c.Attrs,
		&c.ConfidenceReasons, &c.LastSeen, &c.UpdatedAt, &c.Curated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if footprint != nil {
		c.Footprint = json.RawMessage(*footprint)
	}
	if territory != nil {
		c.Territory = json.RawMessage(*territory)
	}
	if levels != nil {
		l := int(*levels)
		c.Levels = &l
	}
	if c.Sources, err = s.sources(ctx, id); err != nil {
		return nil, fmt.Errorf("sources: %w", err)
	}
	if c.Access, err = s.access(ctx, id); err != nil {
		return nil, fmt.Errorf("access: %w", err)
	}
	if c.Restrictions, err = s.restrictions(ctx, id); err != nil {
		return nil, fmt.Errorf("restrictions: %w", err)
	}
	if c.Works, err = s.works(ctx, id); err != nil {
		return nil, fmt.Errorf("works: %w", err)
	}
	if c.Nearby, err = s.nearby(ctx, id); err != nil {
		return nil, fmt.Errorf("nearby: %w", err)
	}
	return &c, nil
}

func (s *Service) sources(ctx context.Context, id uuid.UUID) ([]Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT relation, ref, details FROM infra.facility_source
		WHERE facility_id = $1 ORDER BY relation, ref`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Source, error) {
		var v Source
		return v, r.Scan(&v.Relation, &v.Ref, &v.Details)
	})
}

func (s *Service) access(ctx context.Context, id uuid.UUID) ([]Access, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.profile_code, p.name, a.target_kind, t.name, a.minutes::float8, a.km::float8
		FROM analytics.accessibility a
		JOIN ref.vehicle_profile p ON p.code = a.profile_code
		LEFT JOIN analytics.accessibility_target t ON t.id = a.target_id
		WHERE a.facility_id = $1
		ORDER BY p.ord, array_position(ARRAY['mkad', 'ttk', 'center', 'rail_freight'], a.target_kind)`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Access, error) {
		var v Access
		return v, r.Scan(&v.Profile, &v.ProfileName, &v.Target, &v.TargetName, &v.Minutes, &v.Km)
	})
}

func (s *Service) restrictions(ctx context.Context, id uuid.UUID) ([]Restriction, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.kind, e.value::float8, e.condition, s.name, coalesce(e.origins, '{}'),
		       min(ST_Distance(s.geom::geography, f.point::geography))::float8 AS dist
		FROM infra.facility f
		JOIN network.road_segment s ON s.geom && ST_Expand(f.point, 0.008)
		                            AND ST_DWithin(s.geom::geography, f.point::geography, 500)
		JOIN network.restriction_effective e ON e.segment_id = s.id
		WHERE f.id = $1
		GROUP BY e.kind, e.value, e.condition, s.name, e.origins
		ORDER BY dist LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Restriction, error) {
		var v Restriction
		return v, r.Scan(&v.Kind, &v.Value, &v.Condition, &v.Street, &v.Origins, &v.DistanceM)
	})
}

func (s *Service) works(ctx context.Context, id uuid.UUID) ([]Work, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.title, w.kind, w.state, w.closure, coalesce(w.actual_start, w.planned_start),
		       coalesce(w.actual_end, w.planned_end), rf.distance_m::float8
		FROM operations.road_work_facility rf
		JOIN operations.road_work w ON w.id = rf.work_id
		WHERE rf.facility_id = $1 AND w.state IN ('planned', 'active')
		ORDER BY w.state = 'active' DESC, rf.distance_m LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Work, error) {
		var v Work
		return v, r.Scan(&v.ID, &v.Title, &v.Kind, &v.State, &v.Closure, &v.Starts, &v.Ends, &v.DistanceM)
	})
}

func (s *Service) nearby(ctx context.Context, id uuid.UUID) ([]Nearby, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (p.kind) p.kind, p.name, ST_Distance(p.point::geography, f.point::geography)::float8 AS dist,
		       ST_X(p.point), ST_Y(p.point)
		FROM infra.facility f
		JOIN infra.poi p ON p.point && ST_Expand(f.point, 0.05)
		                AND ST_DWithin(p.point::geography, f.point::geography, 3000)
		WHERE f.id = $1
		ORDER BY p.kind, dist`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Nearby, error) {
		var v Nearby
		return v, r.Scan(&v.Kind, &v.Name, &v.DistanceM, &v.Lon, &v.Lat)
	})
}

// MaxCompare — объектов в сравнении.
const MaxCompare = 4

// Compare возвращает карточки объектов для сравнения в порядке запроса.
func (s *Service) Compare(ctx context.Context, ids []uuid.UUID) ([]*Card, error) {
	if len(ids) < 2 || len(ids) > MaxCompare {
		return nil, fmt.Errorf("%w: compare %d facilities", ErrBadFilter, len(ids))
	}
	out := make([]*Card, len(ids))
	for i, id := range ids {
		c, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
}

// MaxExport — предел строк выгрузки.
const MaxExport = 100_000

// Each обходит всю выборку в порядке сортировки (не больше MaxExport строк).
func (s *Service) Each(ctx context.Context, f Filter, sort string, fn func(Item) error) error {
	order, err := orderBy(sort)
	if err != nil {
		return err
	}
	w, err := f.conditions("")
	if err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+itemColumns+fromClause+`
		WHERE `+w.sql()+` ORDER BY `+order+fmt.Sprintf(` LIMIT %d`, MaxExport), w.args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return err
		}
		if err := fn(it); err != nil {
			return err
		}
	}
	return rows.Err()
}

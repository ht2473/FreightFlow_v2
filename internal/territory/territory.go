// Package territory — округа и районы Москвы: список с показателями и профиль территории.
package territory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/facility"
	"freightflow/internal/platform/cache"
)

// ErrNotFound — территории нет.
var ErrNotFound = errors.New("territory: not found")

// Kinds — уровни территорий списка: административные округа и районы Москвы.
var Kinds = []string{"okrug", "district"}

// Service — территории.
type Service struct {
	pool       *pgxpool.Pool
	facilities *facility.Service
	// list — список с показателями: считается по всем границам (~0,7 с), меняется с загрузками.
	list *cache.TTL[[]Row]
}

// New — сервис территорий; сводка профиля считается сервисом реестра.
func New(pool *pgxpool.Pool, facilities *facility.Service) *Service {
	return &Service{pool: pool, facilities: facilities, list: cache.New[[]Row](5*time.Minute, 4)}
}

// Row — территория с показателями.
type Row struct {
	ID         uuid.UUID
	Kind       string
	Name       string
	ShortName  *string
	ParentID   *uuid.UUID
	ParentName *string
	AreaKm2    float64
	// Population — население на 1 января последнего года (у округа — сумма районов).
	Population     *int
	PopulationYear *int
	Facilities     int
	BuildingM2     float64
	Poi            int
	WorksActive    int
}

// Density — плотность населения, чел./км².
func (r Row) Density() *float64 {
	if r.Population == nil || r.AreaKm2 <= 0 {
		return nil
	}
	d := float64(*r.Population) / r.AreaKm2
	return &d
}

// YearValue — значение ряда по годам.
type YearValue struct {
	Year  int
	Value int
}

// Profile — профиль территории: показатели, граница, ряд населения, районы округа, сводка.
type Profile struct {
	Row
	Geometry   json.RawMessage
	BBox       [4]float64
	Population []YearValue
	Children   []Row
	Summary    *facility.AreaSummary
}

// Объекты — по привязке реестра к округу и району; инфраструктура и работы — по границе.
const rowsHead = `
	WITH pop AS (
		SELECT DISTINCT ON (area_id) area_id, year, value FROM territory.population
		WHERE kind = 'january1' AND area_id IS NOT NULL ORDER BY area_id, year DESC
	)
	SELECT a.id, a.kind, a.name, a.short_name, a.parent_id, coalesce(p.short_name, p.name), a.area_km2,
	       CASE a.kind WHEN 'district' THEN dp.value ELSE op.value END,
	       CASE a.kind WHEN 'district' THEN dp.year ELSE op.year END,
	       fc.n, fc.m2, poi.n, rw.n`

const rowsFrom = `
	FROM territory.admin_area a
	LEFT JOIN territory.admin_area p ON p.id = a.parent_id
	LEFT JOIN pop dp ON dp.area_id = a.id
	LEFT JOIN LATERAL (
		SELECT sum(pop.value)::int AS value, max(pop.year)::int AS year
		FROM territory.admin_area d JOIN pop ON pop.area_id = d.id
		WHERE a.kind = 'okrug' AND d.parent_id = a.id
	) op ON true
	LEFT JOIN LATERAL (
		SELECT count(*)::int AS n, coalesce(sum(f.building_area_m2), 0)::float8 AS m2 FROM infra.facility f
		WHERE f.status = 'active' AND (CASE a.kind WHEN 'district' THEN f.district_id ELSE f.okrug_id END) = a.id
	) fc ON true
	LEFT JOIN LATERAL (
		SELECT count(*)::int AS n FROM infra.poi x WHERE x.point && a.geom AND ST_Intersects(x.point, a.geom)
	) poi ON true
	LEFT JOIN LATERAL (
		SELECT count(*)::int AS n FROM operations.road_work w
		WHERE w.state = 'active' AND w.geom && a.geom AND ST_Intersects(w.geom, a.geom)
	) rw ON true`

func scanRow(r pgx.Row, x *Row, extra ...any) error {
	return r.Scan(append([]any{
		&x.ID, &x.Kind, &x.Name, &x.ShortName, &x.ParentID, &x.ParentName, &x.AreaKm2, &x.Population,
		&x.PopulationYear, &x.Facilities, &x.BuildingM2, &x.Poi, &x.WorksActive,
	}, extra...)...)
}

func (s *Service) rows(ctx context.Context, where string, args ...any) ([]Row, error) {
	rows, err := s.pool.Query(ctx, rowsHead+rowsFrom+` WHERE `+where+` ORDER BY a.kind DESC, a.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("territories: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
		var x Row
		return x, scanRow(r, &x)
	})
}

// List — территории уровня kind (пусто — округа и районы) с показателями.
func (s *Service) List(ctx context.Context, kind string) ([]Row, error) {
	if rows, ok := s.list.Get(kind); ok {
		return rows, nil
	}
	kinds := Kinds
	if kind != "" {
		kinds = []string{kind}
	}
	rows, err := s.rows(ctx, `a.kind = ANY($1)`, kinds)
	if err == nil {
		s.list.Set(kind, rows)
	}
	return rows, err
}

// Get — профиль территории.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Profile, error) {
	var p Profile
	var boundary string
	err := scanRow(s.pool.QueryRow(ctx, rowsHead+`,
		ST_AsGeoJSON(ST_SimplifyPreserveTopology(a.geom, 0.0002), 5), ST_AsGeoJSON(a.geom),
		ST_XMin(a.geom), ST_YMin(a.geom), ST_XMax(a.geom), ST_YMax(a.geom)`+rowsFrom+`
		WHERE a.id = $1 AND a.kind = ANY($2)`, id, Kinds), &p.Row, &p.Geometry, &boundary,
		&p.BBox[0], &p.BBox[1], &p.BBox[2], &p.BBox[3])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT year, sum(value)::int FROM territory.population
		WHERE kind = 'january1' AND area_id IN (SELECT $1::uuid UNION SELECT id FROM territory.admin_area WHERE parent_id = $1)
		GROUP BY year ORDER BY year`, id)
	if err != nil {
		return nil, err
	}
	if p.Population, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (YearValue, error) {
		var v YearValue
		return v, r.Scan(&v.Year, &v.Value)
	}); err != nil {
		return nil, err
	}
	if p.Kind == "okrug" {
		if p.Children, err = s.rows(ctx, `a.parent_id = $1 AND a.kind = 'district'`, id); err != nil {
			return nil, err
		}
	}
	if p.Summary, err = s.facilities.Area(ctx, json.RawMessage(boundary), 10); err != nil {
		return nil, err
	}
	return &p, nil
}

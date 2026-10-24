package facility

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MaxAreaKm2 — предел площади области сводки.
const MaxAreaKm2 = 2500

// AreaSummary — сводка по области на карте: объекты реестра, инфраструктура, ограничения,
// дорожные работы.
type AreaSummary struct {
	AreaKm2      float64
	Facilities   int
	BuildingM2   float64
	FloorM2      float64
	Types        []FacetValue
	Poi          []FacetValue
	Restrictions int     // участков с запретом или пределом
	RestrictedKm float64 // их протяжённость
	WorksActive  int
	WorksPlanned int
	Top          []Item // крупнейшие объекты по площади застройки
}

var poiNames = map[string]string{
	"fuel": "АЗС", "truck_parking": "Стоянки грузовиков", "rest_area": "Площадки отдыха", "marketplace": "Рынки",
	"rail_station": "Вокзалы", "rail_freight": "Ж/д грузовые пункты", "customs": "Таможня", "port": "Порты",
	"fair": "Ярмарки", "tpu": "Транспортно-пересадочные узлы", "technopark": "Технопарки",
}

// Area считает сводку по многоугольнику GeoJSON.
func (s *Service) Area(ctx context.Context, polygon json.RawMessage, top int) (*AreaSummary, error) {
	var out AreaSummary
	var valid bool
	err := s.pool.QueryRow(ctx, `
		WITH g AS (SELECT ST_SetSRID(ST_GeomFromGeoJSON($1), 4326) AS geom)
		SELECT ST_IsValid(geom) AND GeometryType(geom) IN ('POLYGON', 'MULTIPOLYGON'),
		       CASE WHEN ST_IsValid(geom) THEN ST_Area(geom::geography) / 1e6 ELSE 0 END FROM g`, string(polygon)).
		Scan(&valid, &out.AreaKm2)
	if err != nil || !valid {
		return nil, fmt.Errorf("%w: polygon", ErrBadFilter)
	}
	if out.AreaKm2 > MaxAreaKm2 {
		return nil, fmt.Errorf("%w: area %.0f km² over %d", ErrBadFilter, out.AreaKm2, MaxAreaKm2)
	}
	g := string(polygon)
	f := Filter{Polygon: polygon}
	w, _ := f.conditions("")
	if err := s.pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(f.building_area_m2), 0), coalesce(sum(f.floor_area_m2), 0)`+
		fromClause+` WHERE `+w.sql(), w.args...).Scan(&out.Facilities, &out.BuildingM2, &out.FloorM2); err != nil {
		return nil, err
	}
	if out.Types, err = s.facetsOf(ctx, `SELECT f.type_code, t.name, count(*)`+fromClause+` WHERE `+w.sql()+
		` GROUP BY f.type_code, t.name, t.ord ORDER BY t.ord`, w.args...); err != nil {
		return nil, err
	}
	if out.Poi, err = s.facetsOf(ctx, `SELECT kind, kind, count(*) FROM infra.poi
		WHERE ST_Intersects(point, ST_SetSRID(ST_GeomFromGeoJSON($1), 4326)) GROUP BY kind ORDER BY count(*) DESC`, g); err != nil {
		return nil, err
	}
	for i := range out.Poi {
		if l, ok := poiNames[out.Poi[i].Value]; ok {
			out.Poi[i].Label = l
		}
	}
	if err := s.pool.QueryRow(ctx, `
		WITH g AS (SELECT ST_SetSRID(ST_GeomFromGeoJSON($1), 4326) AS geom), seg AS (
			SELECT DISTINCT s.id, ST_Length(ST_Intersection(s.geom, g.geom)::geography) AS m
			FROM network.road_segment s JOIN network.restriction r ON r.segment_id = s.id, g
			WHERE s.geom && g.geom AND ST_Intersects(s.geom, g.geom)
			  AND r.kind IN ('hgv_ban', 'motor_vehicle_ban', 'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength'))
		SELECT count(*), coalesce(sum(m), 0) / 1000 FROM seg`, g).Scan(&out.Restrictions, &out.RestrictedKm); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state = 'active'), count(*) FILTER (WHERE state = 'planned')
		FROM operations.road_work WHERE geom && ST_SetSRID(ST_GeomFromGeoJSON($1), 4326)
		  AND ST_Intersects(geom, ST_SetSRID(ST_GeomFromGeoJSON($1), 4326))`, g).Scan(&out.WorksActive, &out.WorksPlanned); err != nil {
		return nil, err
	}
	page, err := s.List(ctx, f, "-area", "", top)
	if err != nil {
		return nil, err
	}
	out.Top = page.Items
	return &out, nil
}

func (s *Service) facetsOf(ctx context.Context, q string, args ...any) ([]FacetValue, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FacetValue, error) {
		var v FacetValue
		return v, r.Scan(&v.Value, &v.Label, &v.Count)
	})
}

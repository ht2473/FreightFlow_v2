package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service строит маршруты грузовиков с политикой зон и объясняет обходы.
type Service struct {
	pool *pgxpool.Pool
	vh   *Client
}

// NewService создаёт сервис.
func NewService(pool *pgxpool.Pool, vh *Client) *Service { return &Service{pool: pool, vh: vh} }

// Profile — профиль ТС (ref.vehicle_profile или параметры пользователя).
type Profile struct {
	Code, Name                                            string
	GrossT, PayloadT, AxleLoadT, HeightM, WidthM, LengthM float64
	EcoClass                                              int
	Hazmat                                                bool
}

func (p Profile) vehicle() Vehicle {
	return Vehicle{GrossT: p.GrossT, PayloadT: p.PayloadT, EcoClass: p.EcoClass, Hazmat: p.Hazmat}
}

func (p Profile) truck() *Truck {
	return &Truck{Weight: p.GrossT, AxleLoad: p.AxleLoadT, Height: p.HeightM, Width: p.WidthM, Length: p.LengthM, Hazmat: p.Hazmat}
}

// Profiles возвращает типовые профили ТС.
func (s *Service) Profiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT code, name, gross_t::float8, payload_t::float8, axle_load_t::float8, height_m::float8, width_m::float8,
		       length_m::float8, eco_class, hazmat
		FROM ref.vehicle_profile ORDER BY ord, code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Profile, error) {
		var p Profile
		err := row.Scan(&p.Code, &p.Name, &p.GrossT, &p.PayloadT, &p.AxleLoadT, &p.HeightM, &p.WidthM, &p.LengthM,
			&p.EcoClass, &p.Hazmat)
		return p, err
	})
}

// RouteInput — запрос маршрута.
type RouteInput struct {
	Points     []Point
	Profile    Profile
	Passes     []Pass
	Depart     time.Time
	Alternates int
}

// Avoided — ограничение или зона на пути «без ограничений», которых маршрут грузовика избегает.
type Avoided struct {
	Kind     string
	Street   string
	Value    *float64
	Origins  []string
	Segments int
	LengthM  float64
}

// RouteOutput — маршруты грузовика, путь «без ограничений» для сравнения, решения по зонам и
// перечень обойдённого.
type RouteOutput struct {
	Routes   []Route
	Baseline *Route
	Zones    []ZoneDecision
	// Blocked — точки внутри зон, закрытых для этого ТС: маршрут не строится.
	Blocked []BlockedPoint
	// AvoidedZones — закрытые зоны, через которые проходит путь «без ограничений».
	AvoidedZones []string
	Avoided      []Avoided
}

// BlockedPoint — точка маршрута в закрытой зоне.
type BlockedPoint struct {
	Index int
	Zone  string
}

// zoneOrder — зоны от внешней к внутренней: закрытая внешняя зона включает внутренние.
var zoneOrder = []string{"mkad", "ttk", "sk"}

// ErrBlocked — точка маршрута внутри зоны, закрытой для ТС.
var ErrBlocked = errors.New("routing: point inside a forbidden zone")

// Route строит маршрут: решения по зонам, исключение закрытых зон, маршрут грузовика, путь легкового
// автомобиля без ограничений и сравнение их.
func (s *Service) Route(ctx context.Context, in RouteInput) (RouteOutput, error) {
	var out RouteOutput
	rules, err := LoadRules(ctx, s.pool)
	if err != nil {
		return out, fmt.Errorf("zone rules: %w", err)
	}
	depart := in.Depart
	if depart.IsZero() {
		depart = time.Now()
	}
	out.Zones = Decide(rules, in.Profile.vehicle(), in.Passes, depart)
	closed := outermostClosed(out.Zones)
	var exclude []Ring
	if closed != "" {
		rings, err := ZoneRings(ctx, s.pool, []string{closed})
		if err != nil {
			return out, err
		}
		exclude = rings[closed]
		if out.Blocked, err = s.blocked(ctx, in.Points, out.Zones); err != nil {
			return out, err
		}
	}
	baseline, err := s.vh.Route(ctx, RouteRequest{Locations: in.Points, Costing: "auto"})
	if err == nil && len(baseline) > 0 {
		out.Baseline = &baseline[0]
	}
	if len(out.Blocked) > 0 {
		return out, ErrBlocked
	}
	out.Routes, err = s.vh.Route(ctx, RouteRequest{
		Locations: in.Points, Costing: "truck", Truck: in.Profile.truck(), Exclude: exclude,
		Depart: in.Depart, Alternates: in.Alternates,
	})
	if err != nil {
		return out, err
	}
	if out.Baseline != nil {
		if out.Avoided, err = s.avoided(ctx, *out.Baseline, in.Profile); err != nil {
			return out, err
		}
		if out.AvoidedZones, err = s.zonesCrossed(ctx, *out.Baseline, out.Zones); err != nil {
			return out, err
		}
	}
	return out, nil
}

func outermostClosed(ds []ZoneDecision) string {
	closed := map[string]bool{}
	for _, d := range ds {
		if d.Forbidden {
			closed[d.Zone] = true
		}
	}
	for _, z := range zoneOrder {
		if closed[z] {
			return z
		}
	}
	return ""
}

// blocked — точки внутри самой внутренней закрытой зоны, в которой они лежат.
func (s *Service) blocked(ctx context.Context, points []Point, ds []ZoneDecision) ([]BlockedPoint, error) {
	var closed []string
	for _, d := range ds {
		if d.Forbidden {
			closed = append(closed, d.Zone)
		}
	}
	var out []BlockedPoint
	for i, p := range points {
		var zone *string
		err := s.pool.QueryRow(ctx, `
			SELECT code FROM territory.ring_zone
			WHERE code = ANY ($1) AND ST_Intersects(zone, ST_SetSRID(ST_MakePoint($2, $3), 4326))
			ORDER BY ord LIMIT 1`, closed, p.Lon, p.Lat).Scan(&zone)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if zone != nil {
			out = append(out, BlockedPoint{Index: i, Zone: *zone})
		}
	}
	return out, nil
}

func lineJSON(r Route) ([]byte, error) {
	var coords [][2]float64
	for _, l := range r.Legs {
		for _, p := range l.Shape {
			coords = append(coords, [2]float64{p.Lon, p.Lat})
		}
	}
	if len(coords) < 2 {
		return nil, nil
	}
	return json.Marshal(map[string]any{"type": "LineString", "coordinates": coords})
}

// avoided — ограничения действующих данных (ручные, знаки, OSM) на участках пути «без
// ограничений», которые запрещают проезд этому ТС. Участок считается на пути, если линия пути идёт
// вдоль него большей частью длины (в коридоре 8 м).
func (s *Service) avoided(ctx context.Context, baseline Route, p Profile) ([]Avoided, error) {
	line, err := lineJSON(baseline)
	if err != nil || line == nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		WITH b AS (
		    SELECT g, ST_Buffer(g::geography, 8)::geometry AS corridor
		    FROM (SELECT ST_SetSRID(ST_GeomFromGeoJSON($1), 4326) AS g) x
		), seg AS (
		    SELECT s.id, s.name, s.length_m
		    FROM network.road_segment s, b
		    WHERE s.geom && b.corridor AND ST_Intersects(s.geom, b.corridor)
		      AND ST_Length(ST_Intersection(s.geom, b.corridor)::geography) > 0.6 * s.length_m
		)
		SELECT e.kind, coalesce(seg.name, ''), e.value::float8, coalesce(e.origins, '{}'), count(*), sum(seg.length_m)
		FROM seg JOIN network.restriction_effective e ON e.segment_id = seg.id
		WHERE (e.condition IS NULL OR NOT network.time_based(e.condition))
		  AND ((e.kind IN ('hgv_ban', 'motor_vehicle_ban') AND coalesce(e.condition, '') NOT ILIKE '%destination%'
		        AND coalesce(e.condition, '') NOT ILIKE '%въезд%')
		       OR (e.kind = 'hazmat_ban' AND $2)
		       OR (e.kind = 'maxweight' AND e.value < $3)
		       OR (e.kind = 'maxaxleload' AND e.value < $4)
		       OR (e.kind = 'maxheight' AND e.value < $5)
		       OR (e.kind = 'maxwidth' AND e.value < $6)
		       OR (e.kind = 'maxlength' AND e.value < $7))
		GROUP BY e.kind, seg.name, e.value, e.origins
		ORDER BY sum(seg.length_m) DESC
		LIMIT 50`, string(line), p.Hazmat, p.GrossT, p.AxleLoadT, p.HeightM, p.WidthM, p.LengthM)
	if err != nil {
		return nil, fmt.Errorf("avoided restrictions: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Avoided, error) {
		var a Avoided
		err := row.Scan(&a.Kind, &a.Street, &a.Value, &a.Origins, &a.Segments, &a.LengthM)
		return a, err
	})
}

// zonesCrossed — закрытые для ТС зоны, через которые проходит путь «без ограничений»; зона — с
// теми же 40 м, что и при исключении (путь по кольцу тоже требует пропуска).
func (s *Service) zonesCrossed(ctx context.Context, baseline Route, ds []ZoneDecision) ([]string, error) {
	var closed []string
	for _, d := range ds {
		if d.Forbidden {
			closed = append(closed, d.Zone)
		}
	}
	line, err := lineJSON(baseline)
	if err != nil || line == nil || len(closed) == 0 {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT code FROM territory.ring_zone
		WHERE code = ANY ($1)
		  AND ST_Intersects(ST_Buffer(zone::geography, 40)::geometry, ST_SetSRID(ST_GeomFromGeoJSON($2), 4326))
		ORDER BY ord`, closed, string(line))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Status — состояние службы маршрутизации.
func (s *Service) Status(ctx context.Context) (Status, error) { return s.vh.Status(ctx) }

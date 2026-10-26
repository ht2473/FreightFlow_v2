package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

// Расчёты для сценариев «что если»: те же матрицы и изохроны, что в доступности и маршрутах, но
// с исключениями сценария (перекрытые участки и области) и заданными решениями по зонам.

// Exclusions — исключения из графа: многоугольники (области и закрытые зоны) и участки
// (точки с направлением, см. ExcludePoint).
type Exclusions struct {
	Polygons  []Ring
	Locations []ExcludePoint
}

// MaxExcludeLocations — предел исключаемых точек в запросе (service_limits в build.sh).
const MaxExcludeLocations = 200

// ErrTooManyExclusions — перекрытых участков больше, чем принимает служба: большие перекрытия
// задаются областью.
var ErrTooManyExclusions = errors.New("routing: too many excluded edges")

// ProfileByCode — типовой профиль ТС по коду.
func (s *Service) ProfileByCode(ctx context.Context, code string) (Profile, error) {
	return s.profile(ctx, code)
}

// ClosedZone — самая внешняя закрытая зона (она включает внутренние); пусто — все открыты.
func ClosedZone(ds []ZoneDecision) string { return outermostClosed(ds) }

// BlockedPoints — точки внутри зон, закрытых решениями ds.
func (s *Service) BlockedPoints(ctx context.Context, points []Point, ds []ZoneDecision) ([]BlockedPoint, error) {
	return s.blocked(ctx, points, ds)
}

// Rules — действующие правила зон.
func (s *Service) Rules(ctx context.Context) ([]ZoneRule, error) { return LoadRules(ctx, s.pool) }

// ZoneRing — кольца зоны для исключения (с теми же 40 м, что в маршрутах).
func (s *Service) ZoneRing(ctx context.Context, zone string) ([]Ring, error) {
	rings, err := ZoneRings(ctx, s.pool, []string{zone})
	if err != nil {
		return nil, err
	}
	return rings[zone], nil
}

// GraphBuiltAt — время сборки графа службы.
func (s *Service) GraphBuiltAt(ctx context.Context) (time.Time, error) {
	st, err := s.vh.Status(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(st.Tileset, 0), nil
}

// NearestInput — время от источников до ближайшей цели: профиль, исключения, число
// параллельных запросов к службе и отчёт о ходе (выполнено и всего запросов).
type NearestInput struct {
	Sources, Targets []Point
	Profile          Profile
	Exclude          Exclusions
	Workers          int
	Progress         func(done, total int)
}

// Nearest — время (мин) и расстояние (км) от каждого источника до ближайшей цели; -1 —
// недостижимо. Источники привязываются к сети в радиусе facilityRadius, как в доступности.
func (s *Service) Nearest(ctx context.Context, in NearestInput) (minutes, km []float64, err error) {
	minutes, km = make([]float64, len(in.Sources)), make([]float64, len(in.Sources))
	if len(in.Targets) == 0 {
		for i := range minutes {
			minutes[i], km[i] = -1, -1
		}
		return minutes, km, nil
	}
	if len(in.Exclude.Locations) > MaxExcludeLocations {
		return nil, nil, ErrTooManyExclusions
	}
	batch := min(maxSources, max(1, maxPairs/len(in.Targets)))
	total := (len(in.Sources) + batch - 1) / batch
	var done atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, in.Workers))
	for start := 0; start < len(in.Sources); start += batch {
		end := min(start+batch, len(in.Sources))
		g.Go(func() error {
			m, err := s.vh.Matrix(gctx, MatrixRequest{
				Sources: in.Sources[start:end], Targets: in.Targets, Costing: "truck", Truck: in.Profile.truck(),
				SourceRadius: facilityRadius, Exclude: in.Exclude.Polygons, ExcludeLocations: in.Exclude.Locations,
			})
			if err != nil {
				return err
			}
			for i, row := range m {
				bestT, bestL := -1.0, -1.0
				for _, c := range row {
					if c.Reachable && (bestT < 0 || c.Time < bestT) {
						bestT, bestL = c.Time, c.Length
					}
				}
				if bestT >= 0 {
					bestT /= 60
				}
				minutes[start+i], km[start+i] = bestT, bestL
			}
			if in.Progress != nil {
				in.Progress(int(done.Add(1)), total)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	return minutes, km, nil
}

// IsochroneWith — контуры доступности грузовика с исключениями сценария (GeoJSON).
func (s *Service) IsochroneWith(ctx context.Context, origin Point, p Profile, minutes []int, depart time.Time,
	ex Exclusions,
) (json.RawMessage, error) {
	if len(ex.Locations) > MaxExcludeLocations {
		return nil, ErrTooManyExclusions
	}
	return s.vh.Isochrone(ctx, IsochroneRequest{
		Origin: origin, Costing: "truck", Truck: p.truck(), Exclude: ex.Polygons, ExcludeLocations: ex.Locations,
		Minutes: minutes, Depart: depart,
	})
}

// SegmentExclusions — точки исключения рёбер графа анализа, лежащих на участках сети (линиях
// OSM): на четверти длины ребра с его направлением, у двустороннего — и встречным, как при
// проверке критичности. Граф — последний построенный для профиля (если для него нет — любой).
func (s *Service) SegmentExclusions(ctx context.Context, profileCode string, segments []uuid.UUID) ([]ExcludePoint, error) {
	if len(segments) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		WITH v AS (
		    SELECT id FROM network.graph_version ORDER BY profile_code = $1 DESC, built_at DESC LIMIT 1
		)
		SELECT ST_X(p), ST_Y(p), coalesce(heading, 0), forward, backward
		FROM (SELECT e.forward, e.backward, ST_LineInterpolatePoint(e.geom, 0.25) AS p,
		             degrees(ST_Azimuth(ST_LineInterpolatePoint(e.geom, 0.2)::geography,
		                                ST_LineInterpolatePoint(e.geom, 0.3)::geography)) AS heading
		      FROM network.graph_edge e JOIN v ON v.id = e.version_id
		      WHERE e.segment_id = ANY ($2) AND e.length_m > 0) e
		ORDER BY 1, 2`, profileCode, segments)
	if err != nil {
		return nil, fmt.Errorf("segment exclusions: %w", err)
	}
	defer rows.Close()
	var out []ExcludePoint
	for rows.Next() {
		var p Point
		var heading float64
		var fwd, bwd bool
		if err := rows.Scan(&p.Lon, &p.Lat, &heading, &fwd, &bwd); err != nil {
			return nil, err
		}
		// Направление, закрытое профилю, грузовик и так не использует, а точку на нём служба не
		// привязала бы к сети профиля.
		if fwd {
			out = append(out, ExcludePoint{Point: p, Heading: heading})
		}
		if bwd {
			out = append(out, ExcludePoint{Point: p, Heading: math.Mod(heading+180, 360)})
		}
	}
	return out, rows.Err()
}

// CriticalElementRef — элемент рейтинга критичности последнего сетевого анализа.
type CriticalElementRef struct {
	VersionID, ID int
	Kind, Name    string
	Segments      []uuid.UUID
	Rank          int
	// DamageMin — ущерб по рейтингу (подтверждённый матрицами, если проверялся), объекто-минуты.
	DamageMin float64
}

// ErrElementNotFound — элемента нет в последнем сетевом анализе.
var ErrElementNotFound = errors.New("routing: network element not found")

// Element — элемент рейтинга критичности по номеру в последнем анализе.
func (s *Service) Element(ctx context.Context, id int) (CriticalElementRef, error) {
	els, err := s.elements(ctx, `AND c.id = $1`, id)
	if err != nil {
		return CriticalElementRef{}, err
	}
	if len(els) == 0 {
		return CriticalElementRef{}, ErrElementNotFound
	}
	return els[0], nil
}

// TopElements — первые top элементов рейтинга критичности последнего анализа.
func (s *Service) TopElements(ctx context.Context, top int) ([]CriticalElementRef, error) {
	return s.elements(ctx, `AND c.rank <= $1`, top)
}

func (s *Service) elements(ctx context.Context, where string, arg any) ([]CriticalElementRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.version_id, c.id, c.kind, c.name, c.segment_ids, c.rank,
		       coalesce(c.valhalla_damage_min, c.damage_min)::float8
		FROM network.element_criticality c
		WHERE c.version_id = (SELECT max(version_id) FROM network.element_criticality) `+where+`
		ORDER BY c.rank`, arg)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CriticalElementRef, error) {
		var e CriticalElementRef
		err := r.Scan(&e.VersionID, &e.ID, &e.Kind, &e.Name, &e.Segments, &e.Rank, &e.DamageMin)
		return e, err
	})
}

// Targets — цели доступности по видам (МКАД, ТТК, центр, ж/д грузовые пункты).
func (s *Service) Targets(ctx context.Context) (map[string][]Point, error) {
	if _, err := s.pool.Exec(ctx, `SELECT analytics.refresh_accessibility_targets()
		WHERE NOT EXISTS (SELECT 1 FROM analytics.accessibility_target)`); err != nil {
		return nil, fmt.Errorf("targets: %w", err)
	}
	ts, err := s.targets(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]Point, len(ts))
	for kind, list := range ts {
		for _, t := range list {
			out[kind] = append(out[kind], t.Point)
		}
	}
	return out, nil
}

// ElementGeo — элемент рейтинга критичности с геометрией (GeoJSON).
type ElementGeo struct {
	CriticalElementRef
	Geometry json.RawMessage
}

// SearchElements — элементы рейтинга критичности последнего анализа по части названия (пусто —
// все), по рейтингу.
func (s *Service) SearchElements(ctx context.Context, q string, limit int) ([]ElementGeo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.version_id, c.id, c.kind, c.name, c.segment_ids, c.rank,
		       coalesce(c.valhalla_damage_min, c.damage_min)::float8, ST_AsGeoJSON(c.geom, 6)
		FROM network.element_criticality c
		WHERE c.version_id = (SELECT max(version_id) FROM network.element_criticality)
		  AND ($1 = '' OR c.name ILIKE '%' || $1 || '%')
		ORDER BY c.rank LIMIT $2`, q, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ElementGeo, error) {
		var e ElementGeo
		var g string
		err := r.Scan(&e.VersionID, &e.ID, &e.Kind, &e.Name, &e.Segments, &e.Rank, &e.DamageMin, &g)
		e.Geometry = json.RawMessage(g)
		return e, err
	})
}

// SegmentInfo — участок сети (линия OSM).
type SegmentInfo struct {
	ID       uuid.UUID
	Name     string
	Class    string
	LengthM  float64
	Geometry json.RawMessage
}

// ErrNoSegment — у точки нет участка сети.
var ErrNoSegment = errors.New("routing: no road segment near point")

// segmentRadius — радиус поиска участка у точки щелчка, м.
const segmentRadius = 50

// SegmentAt — ближайший к точке участок сети классов маршрутизации в segmentRadius метрах.
func (s *Service) SegmentAt(ctx context.Context, p Point) (SegmentInfo, error) {
	var out SegmentInfo
	var g string
	err := s.pool.QueryRow(ctx, `
		WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326) AS g)
		SELECT s.id, coalesce(s.name, s.ref, ''), s.class, s.length_m::float8, ST_AsGeoJSON(s.geom, 6)
		FROM network.road_segment s, pt
		WHERE s.geom && ST_Expand(pt.g, 0.001) AND ST_DWithin(s.geom::geography, pt.g::geography, $3)
		ORDER BY s.geom <-> pt.g LIMIT 1`, p.Lon, p.Lat, segmentRadius).
		Scan(&out.ID, &out.Name, &out.Class, &out.LengthM, &g)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNoSegment
	}
	out.Geometry = json.RawMessage(g)
	return out, err
}

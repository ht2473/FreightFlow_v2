package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// HotspotParams — параметры поиска горячих точек: показатель ячеек, разрешение, радиус
// соседства в кольцах H3.
type HotspotParams struct {
	Metric string
	Level  string
	K      int
}

// Hotspot — ячейка с оценкой Gi*: z-оценка, уровень значимости (90, 95, 99 % или 0) и знак
// (горячая точка +1, холодная −1).
type Hotspot struct {
	H3         string
	Value      float64
	Density    float64 // значение в окрестности на км²
	Z          float64
	Confidence int
	Sign       int
	Geometry   json.RawMessage
}

// HotspotResult — итог с параметрами для воспроизведения.
type HotspotResult struct {
	Params HotspotParams
	Cells  int
	Mean   float64
	SD     float64
	Hot    int
	Cold   int
	// Significant — значимые ячейки (горячие и холодные) с геометрией.
	Significant []Hotspot
}

// Hotspots — Getis–Ord Gi* (с самой ячейкой в соседстве, бинарные веса диска из K колец) по
// значениям показателя во всех ячейках разрешения; значимость — по нормальному распределению z.
func (s *Service) Hotspots(ctx context.Context, p HotspotParams) (*HotspotResult, error) {
	m, ok := Find(p.Metric)
	if !ok || !strings.HasPrefix(m.Code, "h3.") || !slices.Contains(m.Levels, p.Level) {
		return nil, fmt.Errorf("%w: показатель ячеек %s на уровне %s", ErrUnknown, p.Metric, p.Level)
	}
	if p.K < 1 || p.K > 3 {
		return nil, fmt.Errorf("%w: радиус %d колец", ErrIndexConfig, p.K)
	}
	res := 8
	if p.Level == LevelH3r9 {
		res = 9
	}
	// Значения ячеек и суммы по дискам соседства — в базе (h3_grid_disk).
	rows, err := s.pool.Query(ctx, `
		WITH v AS (
			SELECT c.h3, coalesce(m.value, 0) AS value, c.area_km2
			FROM territory.h3_cell c
			LEFT JOIN analytics.metric_value m ON m.metric_code = $1 AND m.level = $2 AND m.entity = c.h3::text
			WHERE c.res = $3
		)
		SELECT v.h3::text, v.value, sum(n.value), count(n.h3), sum(n.area_km2)
		FROM v
		JOIN LATERAL h3_grid_disk(v.h3, $4) d (h3) ON true
		JOIN v n ON n.h3 = d.h3
		GROUP BY v.h3, v.value`, p.Metric, p.Level, res, p.K)
	if err != nil {
		return nil, err
	}
	type cell struct {
		h3          string
		x, sum, km2 float64
		w           int
	}
	var cs []cell
	for rows.Next() {
		var c cell
		if err := rows.Scan(&c.h3, &c.x, &c.sum, &c.w, &c.km2); err != nil {
			rows.Close()
			return nil, err
		}
		cs = append(cs, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := &HotspotResult{Params: p, Cells: len(cs)}
	n := float64(len(cs))
	if n < 3 {
		return out, nil
	}
	var sum, sq float64
	for _, c := range cs {
		sum += c.x
		sq += c.x * c.x
	}
	mean := sum / n
	sd := math.Sqrt(math.Max(0, sq/n-mean*mean))
	out.Mean, out.SD = mean, sd
	if sd == 0 {
		return out, nil
	}
	var sig []Hotspot
	for _, c := range cs {
		w := float64(c.w) // бинарные веса: Σw = Σw² = число ячеек диска
		den := sd * math.Sqrt((n*w-w*w)/(n-1))
		if den == 0 {
			continue
		}
		z := (c.sum - mean*w) / den
		conf := 0
		switch az := math.Abs(z); {
		case az >= 2.576:
			conf = 99
		case az >= 1.96:
			conf = 95
		case az >= 1.645:
			conf = 90
		}
		if conf == 0 {
			continue
		}
		sign := 1
		if z < 0 {
			sign = -1
			out.Cold++
		} else {
			out.Hot++
		}
		sig = append(sig, Hotspot{H3: c.h3, Value: c.x, Density: c.sum / c.km2, Z: z, Confidence: conf, Sign: sign})
	}
	// Геометрия значимых ячеек.
	ids := make([]string, len(sig))
	for i, h := range sig {
		ids[i] = h.H3
	}
	geoms := map[string]json.RawMessage{}
	grows, err := s.pool.Query(ctx, `SELECT h3::text, ST_AsGeoJSON(geom, 6) FROM territory.h3_cell WHERE h3::text = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for grows.Next() {
		var id, g string
		if err := grows.Scan(&id, &g); err != nil {
			grows.Close()
			return nil, err
		}
		geoms[id] = json.RawMessage(g)
	}
	grows.Close()
	for i := range sig {
		sig[i].Geometry = geoms[sig[i].H3]
	}
	slices.SortFunc(sig, func(a, b Hotspot) int {
		switch {
		case a.Z > b.Z:
			return -1
		case a.Z < b.Z:
			return 1
		}
		return strings.Compare(a.H3, b.H3)
	})
	out.Significant = sig
	return out, grows.Err()
}

// ClusterParams — параметры DBSCAN по объектам реестра: радиус, м; наименьшее число объектов;
// типы объектов (пусто — все).
type ClusterParams struct {
	EpsM      float64
	MinPoints int
	Types     []string
}

// Cluster — группа объектов: число, площадь застройки, центр и оболочка (GeoJSON).
type Cluster struct {
	ID         int
	Facilities int
	BuildingM2 float64
	Lon, Lat   float64
	Hull       json.RawMessage
	Names      []string
}

// ClusterResult — кластеры и объекты вне кластеров.
type ClusterResult struct {
	Params   ClusterParams
	Clusters []Cluster
	Noise    int
	Total    int
}

// Clusters — DBSCAN по точкам действующих объектов (ST_ClusterDBSCAN в проекции 3857; радиус
// переводится в её единицы делением на косинус широты центра Москвы).
func (s *Service) Clusters(ctx context.Context, p ClusterParams) (*ClusterResult, error) {
	if p.EpsM < 50 || p.EpsM > 10000 || p.MinPoints < 2 || p.MinPoints > 100 {
		return nil, fmt.Errorf("%w: радиус %v м, минимум %d", ErrIndexConfig, p.EpsM, p.MinPoints)
	}
	types := p.Types
	if types == nil {
		types = []string{}
	}
	// В проекции Меркатора метр на широте φ растянут в 1/cos φ раз; φ = 55,75° (центр Москвы).
	eps := p.EpsM / math.Cos(55.75*math.Pi/180)
	rows, err := s.pool.Query(ctx, `
		WITH f AS (
			SELECT f.id, coalesce(f.name, t.name) AS name, f.point, f.building_area_m2,
			       ST_ClusterDBSCAN(f.point_3857, eps => $1, minpoints => $2) OVER () AS cid
			FROM infra.facility f JOIN ref.facility_type t ON t.code = f.type_code
			WHERE f.status = 'active' AND (cardinality($3::text[]) = 0 OR f.type_code = ANY($3))
		)
		SELECT cid, count(*)::int, coalesce(sum(building_area_m2), 0)::float8,
		       ST_X(ST_Centroid(ST_Collect(point))), ST_Y(ST_Centroid(ST_Collect(point))),
		       ST_AsGeoJSON(ST_ConvexHull(ST_Collect(point)), 6),
		       (array_agg(name ORDER BY building_area_m2 DESC NULLS LAST))[1:3]
		FROM f GROUP BY cid ORDER BY count(*) DESC`, eps, p.MinPoints, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &ClusterResult{Params: p}
	for rows.Next() {
		var c Cluster
		var cid *int
		var hull string
		if err := rows.Scan(&cid, &c.Facilities, &c.BuildingM2, &c.Lon, &c.Lat, &hull, &c.Names); err != nil {
			return nil, err
		}
		out.Total += c.Facilities
		if cid == nil {
			out.Noise = c.Facilities
			continue
		}
		c.ID = len(out.Clusters) + 1
		c.Hull = json.RawMessage(hull)
		out.Clusters = append(out.Clusters, c)
	}
	return out, rows.Err()
}

package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/wkb"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
)

// AnalysisProfile — профиль ТС сетевого анализа.
const AnalysisProfile = "truck_12"

// keptVersions — сколько последних версий графа хранится.
const keptVersions = 2

// AnalysisStats — итог построения графа и расчёта значимости.
type AnalysisStats struct {
	Version            int
	Nodes, Edges, Arcs int
	LargestComponent   float64
	Profile, Free      Accuracy
	Bottlenecks        int
	NoDetour           int
	Criticality        CriticalityStats
	Duration           time.Duration
}

// CriticalityStats — привязка объектов и точек МКАД к графу, оценённые и проверенные элементы.
type CriticalityStats struct {
	Facilities        int `json:"facilities"`
	FacilitiesSnapped int `json:"facilities_snapped"`
	Targets           int `json:"targets"`
	TargetsSnapped    int `json:"targets_snapped"`
	Elements          int `json:"elements"`
	Bridges           int `json:"bridges"`
	Confirmed         int `json:"confirmed"`
}

// Accuracy — точность выборочной центральности.
type Accuracy struct {
	Sources   int      `json:"sources"`
	Exact     bool     `json:"exact"`
	RSE       *float64 `json:"rse"`
	SplitHalf *float64 `json:"split_half"`
}

func accuracyOf(c Centrality) Accuracy {
	finite := func(v float64) *float64 {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	}
	a := Accuracy{Sources: c.Sources, Exact: c.Exact, SplitHalf: finite(c.SplitHalf)}
	if !c.Exact {
		a.RSE = finite(c.RSE)
	}
	return a
}

// AnalyzeNetwork строит граф для профиля анализа, оценивает центральность рёбер с ограничениями
// профиля и без пределов массы и габаритов, находит узкие места и сохраняет новую версию графа.
func (s *Service) AnalyzeNetwork(ctx context.Context, opts CentralityOptions, progress func(string)) (AnalysisStats, error) {
	var st AnalysisStats
	start := time.Now()
	say := func(format string, args ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, args...))
		}
	}
	profile, err := s.profile(ctx, AnalysisProfile)
	if err != nil {
		return st, err
	}
	var netVersion int64
	if err := s.pool.QueryRow(ctx, `SELECT coalesce((SELECT version FROM ingest.data_version WHERE domain = 'network'), 0)`).
		Scan(&netVersion); err != nil {
		return st, err
	}
	ways, err := loadWays(ctx, s.pool)
	if err != nil {
		return st, fmt.Errorf("ways: %w", err)
	}
	net := BuildNetwork(ways)
	g, dirs := net.Digraph(profile, false)
	free, _ := net.Digraph(profile, true)
	st.Nodes, st.Edges, st.Arcs = len(net.Nodes), len(net.Edges), len(g.arcs)
	st.LargestComponent = g.LargestComponent()
	say("граф %s: узлов %d, рёбер %d, дуг %d, в наибольшей компоненте %.1f %%",
		profile.Name, st.Nodes, st.Edges, st.Arcs, st.LargestComponent*100)

	c, err := g.EdgeCentrality(ctx, len(net.Edges), opts)
	if err != nil {
		return st, err
	}
	st.Profile = accuracyOf(c)
	say("центральность: источников %d, медиана относительной ошибки %.1f %%, устойчивость рангов %.3f",
		c.Sources, c.RSE*100, c.SplitHalf)
	fc, err := free.EdgeCentrality(ctx, len(net.Edges), opts)
	if err != nil {
		return st, err
	}
	st.Free = accuracyOf(fc)
	say("без пределов габаритов: связность %.1f %%, источников %d, медиана относительной ошибки %.1f %%",
		free.LargestComponent()*100, fc.Sources, fc.RSE*100)

	detours := bottlenecks(net, g, free, dirs, fc.Value)
	st.Bottlenecks = len(detours)
	for _, d := range detours {
		if d == nil {
			st.NoDetour++
		}
	}
	say("узких мест по габаритам: %d, из них без объезда %d", st.Bottlenecks, st.NoDetour)

	in, err := criticalityInput(ctx, s.pool, net, g)
	if err != nil {
		return st, fmt.Errorf("criticality input: %w", err)
	}
	els, err := loadElements(ctx, s.pool, net, c)
	if err != nil {
		return st, fmt.Errorf("elements: %w", err)
	}
	if err := g.ScreenCriticality(ctx, in, els); err != nil {
		return st, err
	}
	rankElements(els)
	cs := &st.Criticality
	cs.Facilities, cs.Targets, cs.Elements = len(in.Facilities), len(in.TargetPts), len(els)
	for _, v := range in.FacilityNodes {
		if v >= 0 {
			cs.FacilitiesSnapped++
		}
	}
	for _, v := range in.TargetNodes {
		if v >= 0 {
			cs.TargetsSnapped++
		}
	}
	for _, el := range els {
		if el.Kind == "bridge" {
			cs.Bridges++
		}
	}
	say("критичность: элементов %d (мостов %d); объектов привязано %d из %d, точек МКАД %d из %d",
		cs.Elements, cs.Bridges, cs.FacilitiesSnapped, cs.Facilities, cs.TargetsSnapped, cs.Targets)
	if err := s.confirmCriticality(ctx, profile, net, g, dirs, c, in, els, say); err != nil {
		return st, err
	}
	rankConfirmed(els)
	for _, el := range els {
		if el.Confirmed {
			cs.Confirmed++
		}
	}

	st.Duration = time.Since(start)
	st.Version, err = s.storeAnalysis(ctx, analysisResult{
		profile: profile, netVersion: netVersion, net: net, dirs: dirs, c: c, fc: fc, detours: detours, els: els, stats: st,
	})
	return st, err
}

func (s *Service) profile(ctx context.Context, code string) (Profile, error) {
	ps, err := s.Profiles(ctx)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if p.Code == code {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("vehicle profile %s not found", code)
}

// loadWays читает участки сети классов анализа с ограничениями. Знаки — с однозначной привязкой,
// как при наложении на граф маршрутизации.
func loadWays(ctx context.Context, pool *pgxpool.Pool) ([]Way, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, s.class, coalesce(s.tags->>'highway', '') LIKE '%\_link', s.bridge, s.tunnel, s.oneway,
		       coalesce(s.maxspeed, 0), ST_AsBinary(s.geom),
		       coalesce(jsonb_agg(jsonb_build_object('kind', r.kind, 'value', r.value, 'direction', r.direction,
		                                             'condition', coalesce(r.condition, '')))
		                FILTER (WHERE r.id IS NOT NULL), '[]')
		FROM network.road_segment s
		LEFT JOIN network.restriction r ON r.segment_id = s.id AND (r.origin <> 'sign' OR r.confidence >= $2)
		WHERE s.class = ANY($1)
		GROUP BY s.id
		ORDER BY s.osm_id`, AnalysisClasses, minOverlayConfidence)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Way, error) {
		var w Way
		var oneway int16
		var maxspeed int16
		var geom []byte
		var limits []byte
		if err := r.Scan(&w.Segment, &w.Class, &w.Link, &w.Bridge, &w.Tunnel, &oneway, &maxspeed, &geom, &limits); err != nil {
			return w, err
		}
		w.Oneway, w.MaxSpeed = int(oneway), int(maxspeed)
		g, err := wkb.Unmarshal(geom)
		if err != nil {
			return w, err
		}
		line, ok := g.(orb.LineString)
		if !ok {
			return w, fmt.Errorf("segment %s: geometry %s", w.Segment, g.GeoJSONType())
		}
		w.Line = line
		return w, json.Unmarshal(limits, &w.Limits)
	})
}

// UnmarshalJSON читает ограничение из выборки loadWays.
func (l *Limit) UnmarshalJSON(b []byte) error {
	var v struct {
		Kind      string   `json:"kind"`
		Value     *float64 `json:"value"`
		Direction int      `json:"direction"`
		Condition string   `json:"condition"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*l = Limit(v)
	return nil
}

// bottlenecks — рёбра, закрытые профилю только пределом массы или габарита, через которые без
// предела шли бы кратчайшие пути. Значение — дополнительное время объезда, мин (наибольшее по
// закрытым направлениям); nil — объезда нет. Объезд считается между концами закрытого отрезка:
// путепровод часто разбит на несколько линий, и внутренние точки отрезка открытых проездов не
// имеют.
func bottlenecks(net *Network, g, free *Digraph, dirs []Directions, freeValue []float64) map[int]*float64 {
	out := map[int]*float64{}
	bp, bf := newBrandes(g), newBrandes(free)
	// closed — дуга графа без пределов закрыта профилю.
	closed := func(ai int32) bool {
		a := free.arcs[ai]
		d := dirs[a.edge]
		if a.from == net.Edges[a.edge].From {
			return !d.Forward.Open
		}
		return !d.Backward.Open
	}
	for i, d := range dirs {
		if freeValue[i] <= 0 || !d.Forward.DimsOnly && !d.Backward.DimsOnly {
			continue
		}
		e := &net.Edges[i]
		var worst *float64
		blocked := false
		for _, dir := range []struct {
			pass     Passage
			from, to int32
		}{{d.Forward, e.From, e.To}, {d.Backward, e.To, e.From}} {
			if !dir.pass.DimsOnly {
				continue
			}
			a, b := stretchEnds(g, free, closed, dir.from, dir.to)
			cost, ok := bp.shortest(a, b)
			base, okFree := bf.shortest(a, b)
			if !ok || !okFree {
				blocked = true
				continue
			}
			m := max(0, float64(cost-base)/600)
			if worst == nil || m > *worst {
				worst = &m
			}
		}
		if blocked {
			worst = nil
		}
		out[i] = worst
	}
	return out
}

// stretchEnds — концы закрытого профилю отрезка с ребром u→v: назад от u и вперёд от v по
// закрытым дугам до узлов с открытым профилю проездом.
func stretchEnds(g, free *Digraph, closed func(int32) bool, u, v int32) (a, b int32) {
	const maxSteps = 100
	a, b = u, v
	seen := map[int32]bool{u: true, v: true}
	for range maxSteps {
		if g.outOff[a+1] > g.outOff[a] {
			break
		}
		next := int32(-1)
		for _, ai := range free.inArcs[free.inOff[a]:free.inOff[a+1]] {
			if from := free.arcs[ai].from; closed(ai) && !seen[from] {
				next = from
				break
			}
		}
		if next < 0 {
			break
		}
		seen[next] = true
		a = next
	}
	for range maxSteps {
		if g.inOff[b+1] > g.inOff[b] {
			break
		}
		next := int32(-1)
		for _, ai := range free.outArcs[free.outOff[b]:free.outOff[b+1]] {
			if to := free.arcs[ai].to; closed(ai) && !seen[to] {
				next = to
				break
			}
		}
		if next < 0 {
			break
		}
		seen[next] = true
		b = next
	}
	return a, b
}

type analysisResult struct {
	profile    Profile
	netVersion int64
	net        *Network
	dirs       []Directions
	c, fc      Centrality
	detours    map[int]*float64
	els        []*Element
	stats      AnalysisStats
}

// rankOf — место каждого значения в убывающем порядке (1 — наибольшее).
func rankOf(v []float64) []int32 {
	idx := topIndices(v, len(v))
	r := make([]int32, len(v))
	for i, e := range idx {
		r[e] = int32(i + 1)
	}
	return r
}

// storeAnalysis сохраняет версию графа одной транзакцией, удаляет старые версии и публикует
// домен network_analysis (тайлы значимости и узких мест).
func (s *Service) storeAnalysis(ctx context.Context, r analysisResult) (int, error) {
	acc, err := json.Marshal(map[string]Accuracy{"profile": r.stats.Profile, "free": r.stats.Free})
	if err != nil {
		return 0, err
	}
	crit, err := json.Marshal(r.stats.Criticality)
	if err != nil {
		return 0, err
	}
	var version int
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO network.graph_version (profile_code, network_version, nodes, edges, arcs, largest_component,
			                                   accuracy, criticality, duration_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			r.profile.Code, r.netVersion, r.stats.Nodes, r.stats.Edges, r.stats.Arcs, r.stats.LargestComponent,
			acc, crit, r.stats.Duration.Milliseconds()).Scan(&version); err != nil {
			return err
		}
		if err := copyNodes(ctx, tx, version, r.net); err != nil {
			return fmt.Errorf("nodes: %w", err)
		}
		if err := copyEdges(ctx, tx, version, r); err != nil {
			return fmt.Errorf("edges: %w", err)
		}
		if err := copyMetrics(ctx, tx, version, r); err != nil {
			return fmt.Errorf("metrics: %w", err)
		}
		if err := copyElements(ctx, tx, version, r.els); err != nil {
			return fmt.Errorf("elements: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM network.graph_version
			WHERE id NOT IN (SELECT id FROM network.graph_version ORDER BY id DESC LIMIT $1)`, keptVersions); err != nil {
			return err
		}
		return publishDomain(ctx, tx, "network_analysis")
	})
	return version, err
}

func copyNodes(ctx context.Context, tx pgx.Tx, version int, net *Network) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE node_stage (id integer, lon float8, lat float8) ON COMMIT DROP`); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"node_stage"}, []string{"id", "lon", "lat"},
		pgx.CopyFromSlice(len(net.Nodes), func(i int) ([]any, error) {
			return []any{i, net.Nodes[i][0], net.Nodes[i][1]}, nil
		})); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO network.graph_node (version_id, id, point)
		SELECT $1, id, ST_SetSRID(ST_MakePoint(lon, lat), 4326) FROM node_stage`, version)
	return err
}

func copyEdges(ctx context.Context, tx pgx.Tx, version int, r analysisResult) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE edge_stage (
		id integer, segment_id uuid, from_node integer, to_node integer, length_m real, minutes real,
		forward boolean, backward boolean, closed_by text, closed_value real, dims_only boolean, destination boolean,
		wkb bytea) ON COMMIT DROP`); err != nil {
		return err
	}
	net := r.net
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"edge_stage"}, []string{
		"id", "segment_id", "from_node", "to_node", "length_m", "minutes", "forward", "backward",
		"closed_by", "closed_value", "dims_only", "destination", "wkb",
	}, pgx.CopyFromSlice(len(net.Edges), func(i int) ([]any, error) {
		e := &net.Edges[i]
		w := &net.Ways[e.Way]
		d := r.dirs[i]
		var closedBy *string
		var closedValue *float64
		dimsOnly := false
		for _, p := range []Passage{d.Forward, d.Backward} {
			if p.Open || p.ClosedBy == "oneway" {
				continue
			}
			// Запрет важнее предела габарита.
			if closedBy == nil || !p.DimsOnly && dimsOnly {
				closedBy, dimsOnly = &p.ClosedBy, p.DimsOnly
				closedValue = nil
				if p.DimsOnly {
					closedValue = &p.Value
				}
			}
		}
		line, err := wkb.Marshal(e.Line)
		if err != nil {
			return nil, err
		}
		return []any{
			i, w.Segment, e.From, e.To, float32(e.LengthM), float32(e.LengthM / (w.speed() / 3.6) / 60),
			d.Forward.Open, d.Backward.Open, closedBy, closedValue, dimsOnly,
			d.Forward.Destination || d.Backward.Destination, line,
		}, nil
	})); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO network.graph_edge (version_id, id, segment_id, from_node, to_node, length_m, minutes,
		forward, backward, closed_by, closed_value, dims_only, destination, geom)
		SELECT $1, id, segment_id, from_node, to_node, length_m, minutes, forward, backward, closed_by, closed_value,
		       dims_only, destination, ST_GeomFromWKB(wkb, 4326)
		FROM edge_stage`, version)
	return err
}

func copyMetrics(ctx context.Context, tx pgx.Tx, version int, r analysisResult) error {
	edges := len(r.net.Edges)
	rank, freeRank := rankOf(r.c.Value), rankOf(r.fc.Value)
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"network", "edge_metric"}, []string{
		"version_id", "edge_id", "centrality", "centrality_se", "rank", "percentile",
		"free_centrality", "free_rank", "free_percentile", "bottleneck", "detour_min",
	}, pgx.CopyFromSlice(edges, func(i int) ([]any, error) {
		detour, bottleneck := r.detours[i]
		var minutes *float32
		if detour != nil {
			m := float32(*detour)
			minutes = &m
		}
		return []any{
			version, i, r.c.Value[i], r.c.SE[i], rank[i], float32(rank[i]-1) / float32(edges),
			r.fc.Value[i], freeRank[i], float32(freeRank[i]-1) / float32(edges), bottleneck, minutes,
		}, nil
	}))
	return err
}

func copyElements(ctx context.Context, tx pgx.Tx, version int, els []*Element) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE element_stage (
		id integer, kind text, name text, segment_ids uuid[], edge_ids integer[], centrality float8, rank integer,
		facilities integer, damage_min real, max_delay_min real, disconnected integer, confirmed boolean,
		valhalla_facilities integer, valhalla_damage_min real, valhalla_max_delay_min real, valhalla_disconnected integer
	) ON COMMIT DROP`); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"element_stage"}, []string{
		"id", "kind", "name", "segment_ids", "edge_ids", "centrality", "rank", "facilities", "damage_min",
		"max_delay_min", "disconnected", "confirmed", "valhalla_facilities", "valhalla_damage_min",
		"valhalla_max_delay_min", "valhalla_disconnected",
	}, pgx.CopyFromSlice(len(els), func(i int) ([]any, error) {
		el := els[i]
		row := []any{
			i, el.Kind, el.Name, el.Segments, el.Edges, el.Centrality, i + 1, el.Graph.Facilities,
			float32(el.Graph.Minutes), float32(el.Graph.MaxDelay), el.Graph.Disconnected, el.Confirmed,
			nil, nil, nil, nil,
		}
		if el.Confirmed {
			v := el.Valhalla
			row[12], row[13], row[14], row[15] = v.Facilities, float32(v.Minutes), float32(v.MaxDelay), v.Disconnected
		}
		return row, nil
	})); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO network.element_criticality (version_id, id, kind, name, segment_ids, edge_ids, centrality, rank,
			facilities, damage_min, max_delay_min, disconnected, confirmed, valhalla_facilities, valhalla_damage_min,
			valhalla_max_delay_min, valhalla_disconnected, geom)
		SELECT $1, s.id, s.kind, s.name, s.segment_ids, s.edge_ids, s.centrality, s.rank, s.facilities, s.damage_min,
		       s.max_delay_min, s.disconnected, s.confirmed, s.valhalla_facilities, s.valhalla_damage_min,
		       s.valhalla_max_delay_min, s.valhalla_disconnected,
		       (SELECT ST_Multi(ST_Collect(e.geom)) FROM network.graph_edge e WHERE e.version_id = $1 AND e.id = ANY(s.edge_ids))
		FROM element_stage s`, version)
	return err
}

// publishDomain повышает версию домена данных и сообщает о публикации (кеш тайлов).
func publishDomain(ctx context.Context, tx pgx.Tx, domain string) error {
	var v int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO ingest.data_version (domain, version, published_at) VALUES ($1, 1, now())
		ON CONFLICT (domain) DO UPDATE SET version = ingest.data_version.version + 1, published_at = now()
		RETURNING version`, domain).Scan(&v); err != nil {
		return err
	}
	return events.Publish(ctx, tx, events.DataPublished, map[string]any{"versions": map[string]int64{domain: v}})
}

// NetworkSummary — текущая версия графа анализа: состав, точность, рейтинги.
type NetworkSummary struct {
	Version          int
	Profile          string
	BuiltAt          time.Time
	NetworkVersion   int64
	Nodes, Edges     int
	Arcs             int
	LargestComponent float64
	Profiled, Free   Accuracy
	Duration         time.Duration
	TopStreets       []StreetRank
	Bottlenecks      []Bottleneck
	BottleneckCount  int
	NoDetourCount    int
	Criticality      CriticalityStats
	Critical         []CriticalElement
}

// CriticalElement — мост или участок в рейтинге критичности.
type CriticalElement struct {
	ID         int
	Kind, Name string
	Rank       int
	Graph      Damage
	// Valhalla — проверка матрицами (nil — не проверялся).
	Valhalla *Damage
	Point    Point
	// Same — элементов той же дороги с тем же эффектом (в строке — первый по месту).
	Same int
}

// StreetRank — улица в рейтинге значимости: наибольшая значимость её рёбер и длина в верхних 5 %.
type StreetRank struct {
	Name     string
	Share    float64 // наибольшая доля пар узлов через ребро улицы, %
	BestRank int
	TopKm    float64
	Point    Point
}

// Bottleneck — узкое место по габаритам (участок OSM).
type Bottleneck struct {
	Segment   uuid.UUID
	Name      string
	Kind      string
	Value     *float64
	FreeRank  int
	FreeShare float64 // доля пар узлов, кратчайший путь которых шёл бы через участок без предела, %
	DetourMin *float64
	Point     Point
	// Segments — линий OSM улицы с тем же пределом (в строке — самая значимая).
	Segments int
}

// ErrNoAnalysis — граф анализа ещё не построен.
var ErrNoAnalysis = errors.New("network analysis not built")

// NetworkAnalysis возвращает сводку текущей версии графа: точность, улицы с наибольшей
// значимостью, узкие места по важности путей.
func (s *Service) NetworkAnalysis(ctx context.Context, top int) (*NetworkSummary, error) {
	var sum NetworkSummary
	var acc, crit []byte
	var ms int64
	err := s.pool.QueryRow(ctx, `
		SELECT v.id, p.name, v.built_at, v.network_version, v.nodes, v.edges, v.arcs, v.largest_component, v.accuracy,
		       v.criticality, v.duration_ms
		FROM network.graph_version v JOIN ref.vehicle_profile p ON p.code = v.profile_code
		WHERE v.id = network.current_graph()`).
		Scan(&sum.Version, &sum.Profile, &sum.BuiltAt, &sum.NetworkVersion, &sum.Nodes, &sum.Edges, &sum.Arcs,
			&sum.LargestComponent, &acc, &crit, &ms)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoAnalysis
	}
	if err != nil {
		return nil, err
	}
	sum.Duration = time.Duration(ms) * time.Millisecond
	var a map[string]Accuracy
	if err := json.Unmarshal(acc, &a); err != nil {
		return nil, err
	}
	sum.Profiled, sum.Free = a["profile"], a["free"]
	if err := json.Unmarshal(crit, &sum.Criticality); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT name, share, best_rank, top_km, ST_X(pt), ST_Y(pt)
		FROM (
			SELECT DISTINCT ON (coalesce(s.name, s.ref)) coalesce(s.name, s.ref) AS name,
			       m.centrality * 100 AS share, m.rank AS best_rank,
			       sum(e.length_m) FILTER (WHERE m.percentile <= 0.05) OVER (PARTITION BY coalesce(s.name, s.ref)) / 1000 AS top_km,
			       ST_LineInterpolatePoint(e.geom, 0.5) AS pt
			FROM network.graph_edge e
			JOIN network.edge_metric m ON m.version_id = e.version_id AND m.edge_id = e.id
			JOIN network.road_segment s ON s.id = e.segment_id
			WHERE e.version_id = $1 AND coalesce(s.name, s.ref) IS NOT NULL AND m.percentile <= 0.05
			ORDER BY coalesce(s.name, s.ref), m.rank
		) t
		ORDER BY best_rank LIMIT $2`, sum.Version, top)
	if err != nil {
		return nil, err
	}
	sum.TopStreets, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (StreetRank, error) {
		var v StreetRank
		return v, r.Scan(&v.Name, &v.Share, &v.BestRank, &v.TopKm, &v.Point.Lon, &v.Point.Lat)
	})
	if err != nil {
		return nil, err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT count(DISTINCT e.segment_id), count(DISTINCT e.segment_id) FILTER (WHERE m.detour_min IS NULL)
		FROM network.edge_metric m JOIN network.graph_edge e ON e.version_id = m.version_id AND e.id = m.edge_id
		WHERE m.version_id = $1 AND m.bottleneck`, sum.Version).Scan(&sum.BottleneckCount, &sum.NoDetourCount); err != nil {
		return nil, err
	}
	// Линии одной улицы с тем же пределом — одна строка с их числом; безымянные — по линиям.
	rows, err = s.pool.Query(ctx, `
		WITH seg AS (
			SELECT DISTINCT ON (e.segment_id) e.segment_id, coalesce(s.name, s.ref, '') AS name, e.closed_by,
			       e.closed_value::float8 AS value, m.free_rank, m.free_centrality * 100 AS share,
			       m.detour_min::float8 AS detour, ST_LineInterpolatePoint(e.geom, 0.5) AS pt
			FROM network.edge_metric m
			JOIN network.graph_edge e ON e.version_id = m.version_id AND e.id = m.edge_id
			LEFT JOIN network.road_segment s ON s.id = e.segment_id
			WHERE m.version_id = $1 AND m.bottleneck
			ORDER BY e.segment_id, m.free_rank
		), grouped AS (
			SELECT DISTINCT ON (k) *, count(*) OVER (PARTITION BY k) AS segments
			FROM (SELECT *, CASE WHEN name = '' THEN segment_id::text
			                     ELSE concat_ws('|', name, closed_by, value) END AS k
			      FROM seg) x
			ORDER BY k, free_rank
		)
		SELECT segment_id, name, closed_by, value, free_rank, share, detour, ST_X(pt), ST_Y(pt), segments
		FROM grouped ORDER BY free_rank LIMIT $2`, sum.Version, top)
	if err != nil {
		return nil, err
	}
	sum.Bottlenecks, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Bottleneck, error) {
		var v Bottleneck
		return v, r.Scan(&v.Segment, &v.Name, &v.Kind, &v.Value, &v.FreeRank, &v.FreeShare, &v.DetourMin,
			&v.Point.Lon, &v.Point.Lat, &v.Segments)
	})
	if err != nil {
		return nil, err
	}

	// Сооружения одной дороги с тем же эффектом (мосты трассы на одном перегоне) — одна строка с
	// их числом.
	rows, err = s.pool.Query(ctx, `
		SELECT c.id, c.kind, c.name, c.rank, c.facilities, c.damage_min::float8, c.max_delay_min::float8, c.disconnected,
		       c.confirmed, c.valhalla_facilities, c.valhalla_damage_min::float8, c.valhalla_max_delay_min::float8,
		       c.valhalla_disconnected, ST_X(p), ST_Y(p), c.same
		FROM (
			SELECT DISTINCT ON (k) *, count(*) OVER (PARTITION BY k) AS same
			FROM (SELECT *, CASE WHEN name = '' THEN id::text
			                     ELSE concat_ws('|', kind, name, facilities, round(damage_min), disconnected) END AS k
			      FROM network.element_criticality
			      WHERE version_id = $1 AND (facilities > 0 OR disconnected > 0)) x
			ORDER BY k, rank
		) c, LATERAL ST_LineInterpolatePoint(ST_GeometryN(c.geom, 1), 0.5) p
		ORDER BY c.rank LIMIT $2`, sum.Version, top)
	if err != nil {
		return nil, err
	}
	sum.Critical, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CriticalElement, error) {
		var v CriticalElement
		var confirmed bool
		var vf, vd *int
		var vm, vx *float64
		if err := r.Scan(&v.ID, &v.Kind, &v.Name, &v.Rank, &v.Graph.Facilities, &v.Graph.Minutes, &v.Graph.MaxDelay,
			&v.Graph.Disconnected, &confirmed, &vf, &vm, &vx, &vd, &v.Point.Lon, &v.Point.Lat, &v.Same); err != nil {
			return v, err
		}
		if confirmed && vf != nil && vm != nil && vx != nil && vd != nil {
			v.Valhalla = &Damage{Facilities: *vf, Minutes: *vm, MaxDelay: *vx, Disconnected: *vd}
		}
		return v, nil
	})
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

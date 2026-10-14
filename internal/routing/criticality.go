package routing

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
)

// Критичность элемента сети — насколько вырастает время доступа объектов реестра к МКАД, если
// элемент закрыт. Все мосты и верхние по значимости участки оцениваются по графу анализа
// (обратный поиск от точек МКАД без рёбер элемента); элементы с наибольшим ущербом
// подтверждаются матрицами Valhalla с исключением участков.

const (
	// criticalSegments — участков (линий OSM, кроме мостов) верхней части рейтинга в расчёте.
	criticalSegments = 100
	// confirmTop — элементов с наибольшим ущербом по графу, проверяемых матрицами Valhalla; их
	// места в рейтинге — по результату проверки.
	confirmTop = 30
	// confirmFacilities — предел затронутых объектов элемента в проверке матрицами.
	confirmFacilities = 200
	// snapRadius — радиус привязки объекта и цели к узлу графа, м.
	snapRadius = 500
	// maxExcludeEdges — рёбер элемента в запросе с исключением (две точки на двустороннее ребро;
	// предел службы — 50 точек).
	maxExcludeEdges = 24
)

// Element — элемент сети для оценки критичности: мост (проезжие части одного сооружения) или
// участок дороги (линия OSM).
type Element struct {
	Kind     string // bridge, segment
	Name     string
	Segments []uuid.UUID
	Edges    []int32
	// Centrality — наибольшая значимость ребра элемента.
	Centrality float64
	Graph      Damage
	// Confirmed — проверено матрицами Valhalla (Valhalla — их результат).
	Confirmed bool
	Valhalla  Damage
}

// Damage — последствия закрытия элемента для доступа объектов к МКАД.
type Damage struct {
	// Facilities — объектов с приростом времени; Minutes — суммарный прирост (объекто-минуты).
	Facilities   int
	Minutes      float64
	MaxDelay     float64
	Disconnected int
}

// disconnectedMinutes — объект, теряющий доступ к МКАД, в оценке ущерба: как час объезда.
const disconnectedMinutes = 60

// Score — ущерб для рейтинга: суммарный прирост времени и час за каждый объект без доступа.
func (d Damage) Score() float64 { return d.Minutes + disconnectedMinutes*float64(d.Disconnected) }

// less — элемент с ущербом d критичнее, чем с o.
func (d Damage) less(o Damage) bool {
	if d.Score() != o.Score() {
		return d.Score() > o.Score()
	}
	return d.Facilities > o.Facilities
}

// CriticalityInput — привязка объектов и целей к графу анализа.
type CriticalityInput struct {
	Facilities []uuid.UUID
	// FacilityNodes, TargetNodes — узлы наибольшей компоненты (-1 — не привязан).
	FacilityNodes []int32
	FacilityPts   []Point
	TargetNodes   []int32
	TargetPts     []Point
}

// nodeGrid — узлы по ячейкам сетки для поиска ближайшего.
type nodeGrid struct {
	cell  float64
	cells map[[2]int32][]int32
	pts   []orb.Point
}

const gridCell = 0.005 // градусы, ≈ 300–550 м в Москве

func newNodeGrid(pts []orb.Point, use []bool) *nodeGrid {
	g := &nodeGrid{cell: gridCell, cells: map[[2]int32][]int32{}, pts: pts}
	for i, p := range pts {
		if use[i] {
			k := g.key(p)
			g.cells[k] = append(g.cells[k], int32(i))
		}
	}
	return g
}

func (g *nodeGrid) key(p orb.Point) [2]int32 {
	return [2]int32{int32(math.Floor(p[0] / g.cell)), int32(math.Floor(p[1] / g.cell))}
}

// nearest — ближайший узел в радиусе maxM метров; -1 — нет.
func (g *nodeGrid) nearest(p orb.Point, maxM float64) int32 {
	k := g.key(p)
	dy := int32(math.Ceil(maxM / (g.cell * 111_320)))
	dx := int32(math.Ceil(maxM / (g.cell * 111_320 * math.Cos(p[1]*math.Pi/180))))
	best, bestD := int32(-1), maxM
	for x := k[0] - dx; x <= k[0]+dx; x++ {
		for y := k[1] - dy; y <= k[1]+dy; y++ {
			for _, v := range g.cells[[2]int32{x, y}] {
				if d := geo.Distance(p, g.pts[v]); d <= bestD {
					best, bestD = v, d
				}
			}
		}
	}
	return best
}

// snap привязывает точки к узлам наибольшей сильно связной компоненты графа профиля; local —
// только к узлам местных улиц: подъезд к объекту не идёт с моста, тоннеля, магистрали или съезда,
// иначе закрытие моста «отрезало» бы стоящий рядом объект.
func (n *Network) snap(g *Digraph, pts []Point, local bool) []int32 {
	use := g.inLargest()
	if local {
		street := make([]bool, len(n.Nodes))
		for _, e := range n.Edges {
			w := &n.Ways[e.Way]
			if !w.Bridge && !w.Tunnel && !w.Link && w.Class != "motorway" && w.Class != "trunk" {
				street[e.From], street[e.To] = true, true
			}
		}
		for i := range use {
			use[i] = use[i] && street[i]
		}
	}
	grid := newNodeGrid(n.Nodes, use)
	out := make([]int32, len(pts))
	for i, p := range pts {
		out[i] = grid.nearest(orb.Point{p.Lon, p.Lat}, snapRadius)
	}
	return out
}

// timesTo — время от каждого узла до ближайшей цели: поиск от целей по входящим дугам; дуги с
// blocked[дуга] пропускаются. Результат — в b.dist до b.reset.
func (b *brandes) timesTo(targets []int32, blocked []bool) {
	g := b.g
	for _, t := range targets {
		if t >= 0 && b.dist[t] != 0 {
			if b.dist[t] == math.MaxInt64 {
				b.order = append(b.order, t)
			}
			b.dist[t] = 0
			b.heap.push(0, t)
		}
	}
	for b.heap.len() > 0 {
		d, v := b.heap.pop()
		if d > b.dist[v] {
			continue
		}
		for _, ai := range g.inArcs[g.inOff[v]:g.inOff[v+1]] {
			if blocked != nil && blocked[ai] {
				continue
			}
			a := &g.arcs[ai]
			if nd := d + a.cost; nd < b.dist[a.from] {
				if b.dist[a.from] == math.MaxInt64 {
					b.order = append(b.order, a.from)
				}
				b.dist[a.from] = nd
				b.heap.push(nd, a.from)
			}
		}
	}
}

func (b *brandes) reset() {
	for _, v := range b.order {
		b.dist[v] = math.MaxInt64
	}
	b.order = b.order[:0]
}

// ScreenCriticality оценивает элементы по графу: для каждого — время объектов до ближайшей
// точки МКАД без рёбер элемента против базового.
func (g *Digraph) ScreenCriticality(ctx context.Context, in CriticalityInput, els []*Element) error {
	b := newBrandes(g)
	b.timesTo(in.TargetNodes, nil)
	base := make([]int64, len(in.FacilityNodes))
	for i, v := range in.FacilityNodes {
		base[i] = math.MaxInt64
		if v >= 0 {
			base[i] = b.dist[v]
		}
	}
	b.reset()
	arcsOf := make(map[int32][]int32)
	for ai, a := range g.arcs {
		arcsOf[a.edge] = append(arcsOf[a.edge], int32(ai))
	}

	next := make(chan *Element)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			w := newBrandes(g)
			blocked := make([]bool, len(g.arcs))
			for el := range next {
				for _, e := range el.Edges {
					for _, ai := range arcsOf[e] {
						blocked[ai] = true
					}
				}
				w.timesTo(in.TargetNodes, blocked)
				var d Damage
				for i, v := range in.FacilityNodes {
					if v < 0 || base[i] == math.MaxInt64 {
						continue
					}
					t := w.dist[v]
					switch {
					case t == math.MaxInt64:
						d.Disconnected++
					case t > base[i]:
						delay := float64(t-base[i]) / 600
						d.Facilities++
						d.Minutes += delay
						d.MaxDelay = max(d.MaxDelay, delay)
					}
				}
				w.reset()
				for _, e := range el.Edges {
					for _, ai := range arcsOf[e] {
						blocked[ai] = false
					}
				}
				el.Graph = d
			}
		})
	}
	for _, el := range els {
		if ctx.Err() != nil {
			break
		}
		next <- el
	}
	close(next)
	wg.Wait()
	return ctx.Err()
}

// affected — объекты, чьё время до МКАД по графу растёт без рёбер элемента.
func (g *Digraph) affected(in CriticalityInput, el *Element) []int {
	b := newBrandes(g)
	b.timesTo(in.TargetNodes, nil)
	base := make([]int64, len(in.FacilityNodes))
	for i, v := range in.FacilityNodes {
		base[i] = math.MaxInt64
		if v >= 0 {
			base[i] = b.dist[v]
		}
	}
	b.reset()
	blocked := make([]bool, len(g.arcs))
	for ai, a := range g.arcs {
		if slices.Contains(el.Edges, a.edge) {
			blocked[ai] = true
		}
	}
	b.timesTo(in.TargetNodes, blocked)
	defer b.reset()
	var out []int
	for i, v := range in.FacilityNodes {
		if v >= 0 && base[i] != math.MaxInt64 && b.dist[v] > base[i] {
			out = append(out, i)
		}
	}
	return out
}

// loadElements — мосты (линии OSM на мостах одной дороги в пределах ≈30 м — одно сооружение) и
// верхние по значимости участки, рёбра которых есть в графе.
func loadElements(ctx context.Context, pool *pgxpool.Pool, net *Network, c Centrality) ([]*Element, error) {
	edgesOf := map[uuid.UUID][]int32{}
	for i, e := range net.Edges {
		seg := net.Ways[e.Way].Segment
		edgesOf[seg] = append(edgesOf[seg], int32(i))
	}
	// Кластеры в проекции 3857: 50 единиц — около 28 м на широте Москвы.
	rows, err := pool.Query(ctx, `
		SELECT coalesce(min(b.name), min(s.name), min(s.ref), ''), array_agg(DISTINCT b.segment_id)
		FROM (SELECT name, segment_id,
		             ST_ClusterDBSCAN(geom_3857, eps := 50, minpoints := 1) OVER (PARTITION BY coalesce(name, '')) AS c
		      FROM network.bridge WHERE segment_id IS NOT NULL) b
		JOIN network.road_segment s ON s.id = b.segment_id
		GROUP BY coalesce(b.name, ''), b.c`)
	if err != nil {
		return nil, err
	}
	var els []*Element
	onBridge := map[uuid.UUID]bool{}
	for rows.Next() {
		el := &Element{Kind: "bridge"}
		if err := rows.Scan(&el.Name, &el.Segments); err != nil {
			rows.Close()
			return nil, err
		}
		for _, seg := range el.Segments {
			onBridge[seg] = true
			el.Edges = append(el.Edges, edgesOf[seg]...)
		}
		if len(el.Edges) > 0 {
			els = append(els, el)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type ranked struct {
		seg uuid.UUID
		v   float64
	}
	var segs []ranked
	for seg, es := range edgesOf {
		if onBridge[seg] {
			continue
		}
		best := 0.0
		for _, e := range es {
			best = max(best, c.Value[e])
		}
		if best > 0 {
			segs = append(segs, ranked{seg, best})
		}
	}
	slices.SortFunc(segs, func(a, b ranked) int {
		if a.v != b.v {
			if a.v > b.v {
				return -1
			}
			return 1
		}
		return slices.Compare(a.seg[:], b.seg[:])
	})
	names := map[uuid.UUID]string{}
	top := segs[:min(criticalSegments, len(segs))]
	ids := make([]uuid.UUID, len(top))
	for i, r := range top {
		ids[i] = r.seg
	}
	rows, err = pool.Query(ctx, `SELECT id, coalesce(name, ref, '') FROM network.road_segment WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		names[id] = name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range top {
		els = append(els, &Element{Kind: "segment", Name: names[r.seg], Segments: []uuid.UUID{r.seg}, Edges: edgesOf[r.seg]})
	}
	for _, el := range els {
		for _, e := range el.Edges {
			el.Centrality = max(el.Centrality, c.Value[e])
		}
	}
	return els, nil
}

// criticalityInput — действующие объекты реестра и точки МКАД, привязанные к графу.
func criticalityInput(ctx context.Context, pool *pgxpool.Pool, net *Network, g *Digraph) (CriticalityInput, error) {
	var in CriticalityInput
	// Цели обновляет расчёт доступности (его строки ссылаются на цели); здесь — только если их ещё нет.
	if _, err := pool.Exec(ctx, `SELECT analytics.refresh_accessibility_targets()
		WHERE NOT EXISTS (SELECT 1 FROM analytics.accessibility_target)`); err != nil {
		return in, fmt.Errorf("targets: %w", err)
	}
	// Объекты Москвы: за её пределами в графе только магистрали, и закрытие моста отрезало бы
	// объекты, у которых есть объезд по местным дорогам.
	rows, err := pool.Query(ctx, `
		SELECT f.id, ST_X(f.point), ST_Y(f.point)
		FROM infra.facility f
		JOIN territory.admin_area m ON m.kind = 'region' AND m.name = 'Москва' AND ST_Intersects(m.geom, f.point)
		WHERE f.status = 'active' ORDER BY f.id`)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var id uuid.UUID
		var p Point
		if err := rows.Scan(&id, &p.Lon, &p.Lat); err != nil {
			rows.Close()
			return in, err
		}
		in.Facilities = append(in.Facilities, id)
		in.FacilityPts = append(in.FacilityPts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return in, err
	}
	rows, err = pool.Query(ctx, `SELECT ST_X(point), ST_Y(point) FROM analytics.accessibility_target WHERE kind = 'mkad' ORDER BY id`)
	if err != nil {
		return in, err
	}
	in.TargetPts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Point, error) {
		var p Point
		return p, r.Scan(&p.Lon, &p.Lat)
	})
	if err != nil {
		return in, err
	}
	in.FacilityNodes = net.snap(g, in.FacilityPts, true)
	in.TargetNodes = net.snap(g, in.TargetPts, false)
	return in, nil
}

// excludePoints — точки исключения рёбер элемента для Valhalla: на четверти длины ребра (у
// путепровода середина приходится на дорогу внизу), с направлением ребра; у двустороннего —
// и встречным. Берутся самые значимые рёбра элемента.
func excludePoints(net *Network, dirs []Directions, c Centrality, el *Element) []ExcludePoint {
	edges := slices.Clone(el.Edges)
	slices.SortFunc(edges, func(a, b int32) int {
		switch {
		case c.Value[a] > c.Value[b]:
			return -1
		case c.Value[a] < c.Value[b]:
			return 1
		}
		return int(a - b)
	})
	var out []ExcludePoint
	for _, e := range edges[:min(maxExcludeEdges, len(edges))] {
		p, heading := pointAlong(net.Edges[e].Line, 0.25)
		d := dirs[e]
		if d.Forward.Open {
			out = append(out, ExcludePoint{Point: p, Heading: heading})
		}
		if d.Backward.Open {
			out = append(out, ExcludePoint{Point: p, Heading: math.Mod(heading+180, 360)})
		}
	}
	return out
}

// pointAlong — точка на доле frac длины линии и направление линии в ней (градусы от севера).
func pointAlong(line orb.LineString, frac float64) (Point, float64) {
	total := geo.Length(line)
	want := total * frac
	for i := 1; i < len(line); i++ {
		d := geo.Distance(line[i-1], line[i])
		if d > 0 && (want <= d || i == len(line)-1) {
			t := min(1, want/d)
			p := orb.Point{line[i-1][0] + (line[i][0]-line[i-1][0])*t, line[i-1][1] + (line[i][1]-line[i-1][1])*t}
			b := geo.Bearing(line[i-1], line[i])
			return Point{Lon: p[0], Lat: p[1]}, math.Mod(b+360, 360)
		}
		want -= d
	}
	return Point{Lon: line[0][0], Lat: line[0][1]}, 0
}

// confirmCriticality проверяет элементы с наибольшим ущербом матрицами Valhalla: время
// затронутых объектов до точек МКАД с исключением рёбер элемента и без него.
func (s *Service) confirmCriticality(ctx context.Context, p Profile, net *Network, g *Digraph, dirs []Directions,
	c Centrality, in CriticalityInput, els []*Element, say func(string, ...any),
) error {
	if s.vh == nil {
		return nil
	}
	if _, err := s.vh.Status(ctx); err != nil {
		say("служба маршрутизации недоступна: критичность — только по графу анализа")
		return nil //nolint:nilerr // проверка матрицами необязательна
	}
	// Элементы уже упорядочены rankElements.
	for i, el := range els[:min(confirmTop, len(els))] {
		if el.Graph.Disconnected == 0 && el.Graph.Facilities == 0 {
			break
		}
		idx := g.affected(in, el)
		idx = idx[:min(confirmFacilities, len(idx))]
		if len(idx) == 0 {
			continue
		}
		pts := make([]Point, len(idx))
		for j, k := range idx {
			pts[j] = in.FacilityPts[k]
		}
		before, err := s.nearestTimes(ctx, p, pts, in.TargetPts, nil)
		if err != nil {
			return fmt.Errorf("criticality baseline: %w", err)
		}
		after, err := s.nearestTimes(ctx, p, pts, in.TargetPts, excludePoints(net, dirs, c, el))
		if err != nil {
			return fmt.Errorf("criticality scenario: %w", err)
		}
		var d Damage
		for j := range pts {
			switch {
			case before[j] < 0:
			case after[j] < 0:
				d.Disconnected++
			case after[j] > before[j]:
				delay := (after[j] - before[j]) / 60
				d.Facilities++
				d.Minutes += delay
				d.MaxDelay = max(d.MaxDelay, delay)
			}
		}
		el.Confirmed, el.Valhalla = true, d
		say("%d. %s: по графу %.0f объекто-мин (%d объектов, без доступа %d), по Valhalla %.0f (%d, без доступа %d)",
			i+1, el.Name, el.Graph.Minutes, el.Graph.Facilities, el.Graph.Disconnected, d.Minutes, d.Facilities, d.Disconnected)
	}
	return nil
}

// nearestTimes — время (с) от каждого источника до ближайшей цели; -1 — недостижимо.
func (s *Service) nearestTimes(ctx context.Context, p Profile, sources, targets []Point, exclude []ExcludePoint) ([]float64, error) {
	out := make([]float64, len(sources))
	batch := min(maxSources, max(1, maxPairs/len(targets)))
	for start := 0; start < len(sources); start += batch {
		end := min(start+batch, len(sources))
		m, err := s.vh.Matrix(ctx, MatrixRequest{
			Sources: sources[start:end], Targets: targets, Costing: "truck", Truck: p.truck(),
			SourceRadius: facilityRadius, ExcludeLocations: exclude,
		})
		if err != nil {
			return nil, err
		}
		for i, row := range m {
			best := -1.0
			for _, cell := range row {
				if cell.Reachable && (best < 0 || cell.Time < best) {
					best = cell.Time
				}
			}
			out[start+i] = best
		}
	}
	return out, nil
}

// rankElements упорядочивает элементы по ущербу по графу.
func rankElements(els []*Element) {
	slices.SortStableFunc(els, func(a, b *Element) int { return compareDamage(a.Graph, b.Graph) })
}

// rankConfirmed — итоговый порядок: проверенные матрицами — по результату проверки, затем
// остальные по графу.
func rankConfirmed(els []*Element) {
	slices.SortStableFunc(els, func(a, b *Element) int {
		switch {
		case a.Confirmed && b.Confirmed:
			return compareDamage(a.Valhalla, b.Valhalla)
		case a.Confirmed:
			return -1
		case b.Confirmed:
			return 1
		}
		return compareDamage(a.Graph, b.Graph)
	})
}

func compareDamage(a, b Damage) int {
	switch {
	case a.less(b):
		return -1
	case b.less(a):
		return 1
	}
	return 0
}

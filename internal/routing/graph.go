package routing

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
)

// Граф сетевого анализа: узлы — перекрёстки и концы линий OSM, рёбра — части линий между ними,
// дуги — разрешённые профилю ТС направления проезда с временем в пути.

// AnalysisClasses — классы дорог графа анализа: все, кроме грунтовых. Проезды (без парковочных и
// дворовых — их нет в импорте) нужны для подъезда к объектам: без них часть промзон связана с
// сетью только через мост, и его закрытие ложно «отрезало» бы объекты.
var AnalysisClasses = []string{
	"motorway", "trunk", "primary", "secondary", "tertiary", "unclassified", "residential", "living_street", "service",
}

// classSpeed — скорость по классу дороги без указанного ограничения скорости, км/ч.
var classSpeed = map[string]float64{
	"motorway": 80, "trunk": 60, "primary": 45, "secondary": 40, "tertiary": 35, "unclassified": 30,
	"residential": 20, "living_street": 10, "service": 15,
}

// Пределы скорости грузовика более 3,5 т (ПДД, п. 10.3) и скорость на съездах, км/ч.
const (
	truckMotorwaySpeed = 90
	truckSpeed         = 70
	linkSpeed          = 40
)

// destinationPenalty — множитель времени на участках «только для подъезда»: через них путь идёт,
// лишь когда другого нет.
const destinationPenalty = 5

// Way — линия OSM (участок network.road_segment) с атрибутами графа.
type Way struct {
	Segment uuid.UUID
	Class   string
	Link    bool // съезд (*_link)
	// Bridge, Tunnel — участок на мосту или в тоннеле.
	Bridge, Tunnel bool
	Oneway         int // 1 — по направлению линии, -1 — против, 0 — в обе стороны
	MaxSpeed       int // км/ч; 0 — не указано
	Line           orb.LineString
	Limits         []Limit
}

// Limit — ограничение участка (network.restriction).
type Limit struct {
	Kind      string
	Value     *float64
	Direction int // 1 — по направлению линии, -1 — против, 0 — в обоих
	Condition string
}

// speed — расчётная скорость грузовика, км/ч.
func (w *Way) speed() float64 {
	v := classSpeed[w.Class]
	if w.MaxSpeed > 0 {
		v = float64(w.MaxSpeed)
	}
	if w.Link {
		v = min(v, linkSpeed)
	}
	if w.Class == "motorway" {
		return min(v, truckMotorwaySpeed)
	}
	return min(v, truckSpeed)
}

// Passage — проезд по линии в одном направлении для профиля.
type Passage struct {
	Open bool
	// ClosedBy — вид ограничения, закрывающего проезд (oneway — одностороннее движение).
	ClosedBy string
	// DimsOnly — закрыто только пределами массы и габаритов: без них проезд открыт.
	DimsOnly bool
	// Value — величина предела, закрывающего проезд (т или м).
	Value float64
	// Destination — только для подъезда.
	Destination bool
}

// timeBasedRe — условие по времени: часы в OSM или табличка 8.5.x у знака (как network.time_based).
var timeBasedRe = regexp.MustCompile(`[0-9]{1,2}:[0-9]{2}|8\.5\.`)

// massCondRe — условие по массе из тегов OSM: «weight>12», «maxweight>2.5».
var massCondRe = regexp.MustCompile(`^(?:max)?weight\s*>\s*([0-9]+(?:\.[0-9]+)?)$`)

// dimensionKinds — пределы массы и габаритов в порядке выбора причины закрытия.
var dimensionKinds = []string{"maxheight", "maxweight", "maxaxleload", "maxwidth", "maxlength"}

// limitAbove — значение предела меньше параметра ТС: проезд закрыт.
func limitAbove(l Limit, p Profile) bool {
	if l.Value == nil || *l.Value <= 0 {
		return false
	}
	var have float64
	switch l.Kind {
	case "maxweight":
		have = p.GrossT
	case "maxaxleload":
		have = p.AxleLoadT
	case "maxheight":
		have = p.HeightM
	case "maxwidth":
		have = p.WidthM
	case "maxlength":
		have = p.LengthM
	default:
		return false
	}
	return *l.Value < have
}

// condApplies — условие ограничения без времени выполняется для профиля: пустое, «въезд» или
// масса больше порога. Неизвестный порог (табличка 8.11 без величины) — не выполняется, как при
// наложении знаков на граф Valhalla.
func condApplies(cond string, p Profile) (applies, destination bool) {
	cond = strings.TrimSpace(cond)
	switch cond {
	case "":
		return true, false
	case "въезд":
		return true, true
	}
	if m := massCondRe.FindStringSubmatch(cond); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		return err == nil && p.GrossT > v, false
	}
	return false, false
}

// Pass определяет проезд по линии в направлении dir (1 — по линии, -1 — против). Ограничения по
// времени не закрывают проезд: анализ — о физической проходимости сети, как доступность объектов.
func (w *Way) Pass(dir int, p Profile) Passage {
	if w.Oneway != 0 && w.Oneway != dir {
		return Passage{ClosedBy: "oneway"}
	}
	var res Passage
	ban, dims, dimsValue := "", "", 0.0
	for _, l := range w.Limits {
		if l.Direction != 0 && l.Direction != dir || timeBasedRe.MatchString(l.Condition) {
			continue
		}
		switch l.Kind {
		case "motor_vehicle_ban", "hgv_ban":
			if ok, dest := condApplies(l.Condition, p); ok && dest {
				res.Destination = true
			} else if ok {
				ban = l.Kind
			}
		case "hgv_destination":
			if ok, _ := condApplies(l.Condition, p); ok {
				res.Destination = true
			}
		case "hazmat_ban":
			if p.Hazmat && l.Condition == "" {
				ban = l.Kind
			}
		default:
			if l.Condition != "" || !limitAbove(l, p) {
				continue
			}
			switch {
			case dims == "" || slices.Index(dimensionKinds, l.Kind) < slices.Index(dimensionKinds, dims):
				dims, dimsValue = l.Kind, *l.Value
			case l.Kind == dims:
				dimsValue = min(dimsValue, *l.Value)
			}
		}
	}
	switch {
	case ban != "":
		res.ClosedBy = ban
	case dims != "":
		res.ClosedBy, res.DimsOnly, res.Value = dims, true, dimsValue
	default:
		res.Open = true
	}
	return res
}

// Network — топология графа: узлы и рёбра из линий.
type Network struct {
	Ways  []Way
	Nodes []orb.Point
	Edges []Edge
}

// Edge — часть линии между соседними узлами.
type Edge struct {
	Way      int32
	From, To int32
	Line     orb.LineString
	LengthM  float64
}

// vertexKey — точка с точностью координат OSM (1e-7°): общие точки линий OSM совпадают точно,
// а линии на разных уровнях (мост над дорогой) общих точек не имеют.
type vertexKey [2]int64

func keyOf(p orb.Point) vertexKey {
	return vertexKey{int64(math.Round(p[0] * 1e7)), int64(math.Round(p[1] * 1e7))}
}

// BuildNetwork делит линии на рёбра в общих точках (перекрёстках) и концах.
func BuildNetwork(ways []Way) *Network {
	uses := make(map[vertexKey]int32, len(ways)*4)
	for _, w := range ways {
		for _, p := range w.Line {
			uses[keyOf(p)]++
		}
	}
	n := &Network{Ways: ways}
	ids := make(map[vertexKey]int32, len(ways)*2)
	next := int32(0)
	node := func(p orb.Point) int32 {
		k := keyOf(p)
		if id, ok := ids[k]; ok {
			return id
		}
		id := next
		next++
		ids[k] = id
		n.Nodes = append(n.Nodes, p)
		return id
	}
	for wi, w := range ways {
		if len(w.Line) < 2 {
			continue
		}
		start := 0
		for i := 1; i < len(w.Line); i++ {
			if i < len(w.Line)-1 && uses[keyOf(w.Line[i])] < 2 {
				continue
			}
			line := w.Line[start : i+1]
			from, to := node(line[0]), node(line[len(line)-1])
			if from != to {
				n.Edges = append(n.Edges, Edge{
					Way: int32(wi), From: from, To: to,
					Line: line, LengthM: geo.Length(line),
				})
			}
			start = i
		}
	}
	return n
}

// arc — направление проезда по ребру; cost — время в десятых долях секунды (не меньше 1).
type arc struct {
	from, to, edge int32
	cost           int64
}

// Digraph — ориентированный граф для профиля в виде списков смежности (CSR).
type Digraph struct {
	n       int
	arcs    []arc
	outOff  []int32
	outArcs []int32
	inOff   []int32
	inArcs  []int32
}

// Directions — разрешённые профилю направления ребра.
type Directions struct {
	Forward, Backward Passage
}

// Digraph строит граф проезда для профиля. ignoreDims — без пределов массы и габаритов (для
// оценки узких мест: какие пути шли бы через участок без ограничения).
func (n *Network) Digraph(p Profile, ignoreDims bool) (*Digraph, []Directions) {
	dirs := make([]Directions, len(n.Edges))
	arcs := make([]arc, 0, len(n.Edges)*2)
	for i, e := range n.Edges {
		w := &n.Ways[e.Way]
		fwd, bwd := w.Pass(1, p), w.Pass(-1, p)
		dirs[i] = Directions{Forward: fwd, Backward: bwd}
		seconds := e.LengthM / (w.speed() / 3.6)
		for _, d := range []struct {
			pass     Passage
			from, to int32
		}{{fwd, e.From, e.To}, {bwd, e.To, e.From}} {
			if !d.pass.Open && (!ignoreDims || !d.pass.DimsOnly) {
				continue
			}
			s := seconds
			if d.pass.Destination {
				s *= destinationPenalty
			}
			arcs = append(arcs, arc{from: d.from, to: d.to, edge: int32(i), cost: max(1, int64(math.Round(s*10)))})
		}
	}
	return newDigraph(len(n.Nodes), arcs), dirs
}

func newDigraph(n int, arcs []arc) *Digraph {
	g := &Digraph{n: n, arcs: arcs}
	g.outOff, g.outArcs = index(n, arcs, func(a arc) int32 { return a.from })
	g.inOff, g.inArcs = index(n, arcs, func(a arc) int32 { return a.to })
	return g
}

// index — смещения и номера дуг, упорядоченные по узлу key (сортировка подсчётом).
func index(n int, arcs []arc, key func(arc) int32) (off, idx []int32) {
	off = make([]int32, n+1)
	for _, a := range arcs {
		off[key(a)+1]++
	}
	for i := range n {
		off[i+1] += off[i]
	}
	pos := make([]int32, n)
	copy(pos, off[:n])
	idx = make([]int32, len(arcs))
	for i, a := range arcs {
		k := key(a)
		idx[pos[k]] = int32(i)
		pos[k]++
	}
	return off, idx
}

// LargestComponent — доля узлов в наибольшей сильно связной компоненте (из любого узла в любой
// можно проехать): мера связности графа профиля.
func (g *Digraph) LargestComponent() float64 {
	if g.n == 0 {
		return 0
	}
	in := g.inLargest()
	n := 0
	for _, ok := range in {
		if ok {
			n++
		}
	}
	return float64(n) / float64(g.n)
}

// inLargest отмечает узлы наибольшей сильно связной компоненты (алгоритм Косараю: порядок выхода
// обхода по исходящим дугам, затем обходы по входящим).
func (g *Digraph) inLargest() []bool {
	seen := make([]bool, g.n)
	order := make([]int32, 0, g.n)
	type frame struct{ v, next int32 }
	var stack []frame
	for s := int32(0); int(s) < g.n; s++ {
		if seen[s] {
			continue
		}
		seen[s] = true
		stack = append(stack, frame{s, g.outOff[s]})
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.next < g.outOff[f.v+1] {
				w := g.arcs[g.outArcs[f.next]].to
				f.next++
				if !seen[w] {
					seen[w] = true
					stack = append(stack, frame{w, g.outOff[w]})
				}
				continue
			}
			order = append(order, f.v)
			stack = stack[:len(stack)-1]
		}
	}
	comp := make([]int32, g.n)
	for i := range comp {
		comp[i] = -1
	}
	best, bestSize := int32(-1), 0
	var queue []int32
	for i := len(order) - 1; i >= 0; i-- {
		s := order[i]
		if comp[s] >= 0 {
			continue
		}
		comp[s] = s
		size := 0
		queue = append(queue[:0], s)
		for len(queue) > 0 {
			v := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			size++
			for _, ai := range g.inArcs[g.inOff[v]:g.inOff[v+1]] {
				if u := g.arcs[ai].from; comp[u] < 0 {
					comp[u] = s
					queue = append(queue, u)
				}
			}
		}
		if size > bestSize {
			best, bestSize = s, size
		}
	}
	in := make([]bool, g.n)
	for v, c := range comp {
		in[v] = c == best
	}
	return in
}

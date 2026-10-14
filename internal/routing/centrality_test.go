package routing

import (
	"math"
	"math/rand/v2"
	"testing"

	"gonum.org/v1/gonum/graph/network"
	"gonum.org/v1/gonum/graph/path"
	"gonum.org/v1/gonum/graph/simple"
)

// grid — решётка size×size: рёбра между соседями с весом из weight; каждое пятое ребро
// одностороннее. Возвращает граф и число рёбер.
func grid(size int, weight func() int64) (*Digraph, int) {
	var arcs []arc
	edges := int32(0)
	id := func(x, y int) int32 { return int32(y*size + x) }
	add := func(u, v int32) {
		w := weight()
		arcs = append(arcs, arc{from: u, to: v, edge: edges, cost: w})
		if edges%5 != 0 {
			arcs = append(arcs, arc{from: v, to: u, edge: edges, cost: w})
		}
		edges++
	}
	for y := range size {
		for x := range size {
			if x+1 < size {
				add(id(x, y), id(x+1, y))
			}
			if y+1 < size {
				add(id(x, y), id(x, y+1))
			}
		}
	}
	return newDigraph(size*size, arcs), int(edges)
}

// exactGonum — точная центральность рёбер по gonum (все пары, все кратчайшие пути), нормированная
// как в EdgeCentrality.
func exactGonum(g *Digraph, edges int) []float64 {
	wg := simple.NewWeightedDirectedGraph(0, math.Inf(1))
	for i := range g.n {
		wg.AddNode(simple.Node(int64(i)))
	}
	byArc := map[[2]int64]int32{}
	for _, a := range g.arcs {
		wg.SetWeightedEdge(wg.NewWeightedEdge(simple.Node(int64(a.from)), simple.Node(int64(a.to)), float64(a.cost)))
		byArc[[2]int64{int64(a.from), int64(a.to)}] = a.edge
	}
	out := make([]float64, edges)
	norm := 1 / (float64(g.n) * float64(g.n-1))
	for k, v := range network.EdgeBetweennessWeighted(wg, path.DijkstraAllPaths(wg)) {
		out[byArc[k]] += v * norm
	}
	return out
}

func TestEdgeCentralityExactMatchesGonum(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	// Веса 1–2: много равных по времени путей — проверка подсчёта их числа.
	g, edges := grid(7, func() int64 { return 1 + rng.Int64N(2) })
	got, err := g.EdgeCentrality(t.Context(), edges, CentralityOptions{MaxSources: 1000, Workers: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exact || got.Sources != 49 {
		t.Fatalf("exact %v, sources %d", got.Exact, got.Sources)
	}
	want := exactGonum(g, edges)
	for e := range edges {
		if math.Abs(got.Value[e]-want[e]) > 1e-12 {
			t.Fatalf("edge %d: %g, gonum %g", e, got.Value[e], want[e])
		}
	}
}

func TestEdgeCentralitySampled(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(3, 4))
	g, edges := grid(30, func() int64 { return 10 + rng.Int64N(90) })
	exact, err := g.EdgeCentrality(t.Context(), edges, CentralityOptions{MaxSources: 1000})
	if err != nil {
		t.Fatal(err)
	}
	est, err := g.EdgeCentrality(t.Context(), edges, CentralityOptions{
		Batch: 20, MinSources: 160, MaxSources: 400, TargetRSE: 0.05, Top: 50, Seed: 7, Workers: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if est.Exact || est.Sources < 160 || est.Sources > 400 || est.Sources%20 != 0 {
		t.Fatalf("sources %d, exact %v", est.Sources, est.Exact)
	}
	top := topIndices(exact.Value, 100)
	x, y := make([]float64, len(top)), make([]float64, len(top))
	covered := 0
	for i, e := range top {
		x[i], y[i] = exact.Value[e], est.Value[e]
		if math.Abs(est.Value[e]-exact.Value[e]) <= 3*est.SE[e] {
			covered++
		}
	}
	// Оценка близка к точной по рангам, а стандартная ошибка честно описывает отклонение.
	if r := Spearman(x, y); r < 0.8 {
		t.Errorf("spearman with exact %.3f", r)
	}
	if covered < 90 {
		t.Errorf("exact within 3 SE for %d of 100 top edges", covered)
	}
	if math.IsInf(est.RSE, 0) || est.RSE <= 0 || est.SplitHalf < 0.5 {
		t.Errorf("rse %.3f, split-half %.3f", est.RSE, est.SplitHalf)
	}
}

func TestShortest(t *testing.T) {
	t.Parallel()
	// 0 → 1 → 2 дешевле прямой дуги 0 → 2; обратно из 2 пути нет.
	g := newDigraph(3, []arc{{0, 1, 0, 5}, {1, 2, 1, 5}, {0, 2, 2, 20}})
	if d, ok := g.Shortest(0, 2); !ok || d != 10 {
		t.Errorf("0→2: %d %v", d, ok)
	}
	if _, ok := g.Shortest(2, 0); ok {
		t.Error("2→0 reachable")
	}
}

func TestSpearman(t *testing.T) {
	t.Parallel()
	if r := Spearman([]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}); math.Abs(r-1) > 1e-12 {
		t.Errorf("monotone: %g", r)
	}
	if r := Spearman([]float64{1, 2, 3, 4}, []float64{4, 3, 2, 1}); math.Abs(r+1) > 1e-12 {
		t.Errorf("reverse: %g", r)
	}
	if got := ranks([]float64{5, 1, 5, 3}); got[0] != 3.5 || got[1] != 1 || got[2] != 3.5 || got[3] != 2 {
		t.Errorf("ties: %v", got)
	}
}

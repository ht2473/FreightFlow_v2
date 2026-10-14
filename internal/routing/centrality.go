package routing

import (
	"context"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"sort"
	"sync"

	"gonum.org/v1/gonum/stat"
)

// CentralityOptions — параметры выборочной оценки рёберной центральности.
type CentralityOptions struct {
	// Batch — источников в порции; разброс оценок порций даёт стандартную ошибку.
	Batch int
	// MinSources, MaxSources — пределы числа источников; граф не больше MaxSources узлов
	// считается точно (все узлы — источники).
	MinSources, MaxSources int
	// TargetRSE — целевая медиана относительной стандартной ошибки по Top рёбрам.
	TargetRSE float64
	Top       int
	Seed      uint64
	Workers   int
}

// DefaultCentrality — параметры для сети Москвы: 1–2 тыс. источников дают медиану
// относительной ошибки верхней сотни рёбер в несколько процентов.
var DefaultCentrality = CentralityOptions{
	Batch: 64, MinSources: 512, MaxSources: 4096, TargetRSE: 0.05, Top: 100, Seed: 20261013,
}

// Centrality — оценка рёберной центральности: доля упорядоченных пар узлов, кратчайшие по
// времени пути между которыми проходят через ребро (в обоих направлениях вместе).
type Centrality struct {
	Value []float64
	// SE — стандартная ошибка оценки (0 при точном расчёте).
	SE      []float64
	Sources int
	Exact   bool
	// RSE — медиана относительной стандартной ошибки по верхним рёбрам.
	RSE float64
	// SplitHalf — ранговая корреляция Спирмена оценок по чётным и нечётным порциям источников
	// на верхней тысяче рёбер: устойчивость рейтинга.
	SplitHalf float64
}

// brandes — рабочие массивы одного обхода (на поток).
type brandes struct {
	g     *Digraph
	dist  []int64
	sigma []float64
	delta []float64
	order []int32
	heap  distHeap
}

func newBrandes(g *Digraph) *brandes {
	b := &brandes{g: g, dist: make([]int64, g.n), sigma: make([]float64, g.n), delta: make([]float64, g.n)}
	for i := range b.dist {
		b.dist[i] = math.MaxInt64
	}
	return b
}

// accumulate добавляет в acc (по рёбрам) зависимости источника s: алгоритм Брандеса для
// взвешенного графа — дерево кратчайших путей с числом путей, затем обратный проход.
func (b *brandes) accumulate(s int32, acc []float64) {
	g := b.g
	b.dist[s], b.sigma[s] = 0, 1
	b.heap.push(0, s)
	for b.heap.len() > 0 {
		d, v := b.heap.pop()
		if d > b.dist[v] {
			continue
		}
		b.order = append(b.order, v)
		for _, ai := range g.outArcs[g.outOff[v]:g.outOff[v+1]] {
			a := &g.arcs[ai]
			nd := d + a.cost
			switch {
			case nd < b.dist[a.to]:
				b.dist[a.to], b.sigma[a.to] = nd, b.sigma[v]
				b.heap.push(nd, a.to)
			case nd == b.dist[a.to]:
				b.sigma[a.to] += b.sigma[v]
			}
		}
	}
	for i := len(b.order) - 1; i >= 0; i-- {
		w := b.order[i]
		for _, ai := range g.inArcs[g.inOff[w]:g.inOff[w+1]] {
			a := &g.arcs[ai]
			if b.dist[a.from] == math.MaxInt64 || b.dist[a.from]+a.cost != b.dist[w] {
				continue
			}
			c := b.sigma[a.from] / b.sigma[w] * (1 + b.delta[w])
			acc[a.edge] += c
			b.delta[a.from] += c
		}
	}
	for _, v := range b.order {
		b.dist[v], b.sigma[v], b.delta[v] = math.MaxInt64, 0, 0
	}
	b.order = b.order[:0]
}

// EdgeCentrality оценивает центральность рёбер (edges — число рёбер сети). Источники берутся
// случайно без повторов порциями; после каждого раунда по разбросу оценок порций проверяется
// точность, расчёт останавливается при TargetRSE или MaxSources.
func (g *Digraph) EdgeCentrality(ctx context.Context, edges int, o CentralityOptions) (Centrality, error) {
	workers := o.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	n := g.n
	res := Centrality{Value: make([]float64, edges), SE: make([]float64, edges)}
	if n < 2 {
		res.Exact = true
		return res, nil
	}
	norm := 1 / (float64(n) * float64(n-1))
	if n <= o.MaxSources {
		sources := make([]int32, n)
		for i := range sources {
			sources[i] = int32(i)
		}
		acc, err := g.dependencies(ctx, sources, edges, workers)
		if err != nil {
			return res, err
		}
		for e, v := range acc {
			res.Value[e] = v * norm
		}
		res.Sources, res.Exact, res.SplitHalf = n, true, 1
		return res, nil
	}

	// Случайный порядок узлов: порция k — источники order[k·batch : (k+1)·batch].
	order := make([]int32, n)
	for i := range order {
		order[i] = int32(i)
	}
	rng := rand.New(rand.NewPCG(o.Seed, o.Seed^0x9e3779b97f4a7c15)) //nolint:gosec // выборка, не криптография
	rng.Shuffle(n, func(a, b int) { order[a], order[b] = order[b], order[a] })
	batch := o.Batch
	sum := make([]float64, edges)
	sumsq := make([]float64, edges)
	half := [2][]float64{make([]float64, edges), make([]float64, edges)}
	batches := 0
	for {
		var round [][]int32
		for range max(workers, 2) {
			lo := (batches + len(round)) * batch
			if lo+batch > min(n, o.MaxSources) {
				break
			}
			round = append(round, order[lo:lo+batch])
		}
		if len(round) == 0 {
			break
		}
		// Оценка порции: (n / batch) · сумма зависимостей её источников.
		scale := float64(n) / float64(batch)
		err := g.batches(ctx, round, edges, workers, func(i int, acc []float64) {
			k := (batches + i) % 2
			for e, v := range acc {
				if v == 0 {
					continue
				}
				v *= scale
				sum[e] += v
				sumsq[e] += v * v
				half[k][e] += v
			}
		})
		if err != nil {
			return res, err
		}
		batches += len(round)
		if batches*batch < o.MinSources {
			continue
		}
		res.RSE = medianRSE(sum, sumsq, batches, o.Top)
		if res.RSE <= o.TargetRSE {
			break
		}
	}
	b := float64(batches)
	for e := range sum {
		mean := sum[e] / b
		res.Value[e] = mean * norm
		if batches > 1 {
			v := max(0, (sumsq[e]-b*mean*mean)/(b-1))
			res.SE[e] = math.Sqrt(v/b) * norm
		}
	}
	res.Sources = batches * batch
	res.RSE = medianRSE(sum, sumsq, batches, o.Top)
	res.SplitHalf = splitHalf(res.Value, half, 1000)
	return res, nil
}

// batches считает зависимости порций источников в workers потоках; merge вызывается под
// блокировкой для каждой порции (номер порции в раунде, суммы по рёбрам).
func (g *Digraph) batches(ctx context.Context, round [][]int32, edges, workers int, merge func(int, []float64)) error {
	var mu sync.Mutex
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(workers, len(round)) {
		wg.Go(func() {
			b := newBrandes(g)
			acc := make([]float64, edges)
			for i := range next {
				clear(acc)
				for _, s := range round[i] {
					if ctx.Err() != nil {
						break
					}
					b.accumulate(s, acc)
				}
				mu.Lock()
				merge(i, acc)
				mu.Unlock()
			}
		})
	}
	for i := range round {
		next <- i
	}
	close(next)
	wg.Wait()
	return ctx.Err()
}

// dependencies — сумма зависимостей всех источников, поделённых между потоками.
func (g *Digraph) dependencies(ctx context.Context, sources []int32, edges, workers int) ([]float64, error) {
	step := (len(sources) + workers - 1) / workers
	total := make([]float64, edges)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for lo := 0; lo < len(sources); lo += step {
		part := sources[lo:min(lo+step, len(sources))]
		wg.Go(func() {
			b := newBrandes(g)
			acc := make([]float64, edges)
			for _, s := range part {
				if ctx.Err() != nil {
					return
				}
				b.accumulate(s, acc)
			}
			mu.Lock()
			for e, v := range acc {
				total[e] += v
			}
			mu.Unlock()
		})
	}
	wg.Wait()
	return total, ctx.Err()
}

// medianRSE — медиана относительной стандартной ошибки по top рёбрам с наибольшей оценкой.
func medianRSE(sum, sumsq []float64, batches, top int) float64 {
	if batches < 2 {
		return math.Inf(1)
	}
	b := float64(batches)
	idx := topIndices(sum, top)
	rse := make([]float64, 0, len(idx))
	for _, e := range idx {
		mean := sum[e] / b
		if mean <= 0 {
			continue
		}
		v := max(0, (sumsq[e]-b*mean*mean)/(b-1))
		rse = append(rse, math.Sqrt(v/b)/mean)
	}
	if len(rse) == 0 {
		return math.Inf(1)
	}
	sort.Float64s(rse)
	return rse[len(rse)/2]
}

// topIndices — номера k наибольших значений.
func topIndices(v []float64, k int) []int {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int {
		switch {
		case v[a] > v[b]:
			return -1
		case v[a] < v[b]:
			return 1
		}
		return a - b
	})
	return idx[:min(k, len(idx))]
}

// splitHalf — корреляция Спирмена оценок двух половин порций на top рёбрах полной оценки.
func splitHalf(value []float64, half [2][]float64, top int) float64 {
	idx := topIndices(value, top)
	x, y := make([]float64, len(idx)), make([]float64, len(idx))
	for i, e := range idx {
		x[i], y[i] = half[0][e], half[1][e]
	}
	return Spearman(x, y)
}

// Spearman — ранговая корреляция: корреляция Пирсона средних рангов.
func Spearman(x, y []float64) float64 {
	if len(x) < 2 {
		return math.NaN()
	}
	return stat.Correlation(ranks(x), ranks(y), nil)
}

// ranks — ранги значений (1 — наименьшее); равные получают средний ранг.
func ranks(v []float64) []float64 {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
	r := make([]float64, len(v))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && v[idx[j+1]] == v[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

// distHeap — двоичная куча (время, узел) с ленивым удалением устаревших записей.
type distHeap struct {
	d []int64
	v []int32
}

func (h *distHeap) len() int { return len(h.d) }

func (h *distHeap) push(d int64, v int32) {
	h.d, h.v = append(h.d, d), append(h.v, v)
	i := len(h.d) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h.d[p] <= h.d[i] {
			break
		}
		h.d[p], h.d[i] = h.d[i], h.d[p]
		h.v[p], h.v[i] = h.v[i], h.v[p]
		i = p
	}
}

func (h *distHeap) pop() (int64, int32) {
	d, v := h.d[0], h.v[0]
	last := len(h.d) - 1
	h.d[0], h.v[0] = h.d[last], h.v[last]
	h.d, h.v = h.d[:last], h.v[:last]
	i := 0
	for {
		l, m := 2*i+1, i
		if l < last && h.d[l] < h.d[m] {
			m = l
		}
		if r := l + 1; r < last && h.d[r] < h.d[m] {
			m = r
		}
		if m == i {
			break
		}
		h.d[m], h.d[i] = h.d[i], h.d[m]
		h.v[m], h.v[i] = h.v[i], h.v[m]
		i = m
	}
	return d, v
}

// Shortest — время кратчайшего пути от s до t (десятые доли секунды); false — недостижимо.
func (g *Digraph) Shortest(s, t int32) (int64, bool) {
	b := newBrandes(g)
	return b.shortest(s, t)
}

func (b *brandes) shortest(s, t int32) (int64, bool) {
	g := b.g
	defer func() {
		for _, v := range b.order {
			b.dist[v] = math.MaxInt64
		}
		b.order = b.order[:0]
		b.heap.d, b.heap.v = b.heap.d[:0], b.heap.v[:0]
	}()
	b.dist[s] = 0
	b.order = append(b.order, s)
	b.heap.push(0, s)
	for b.heap.len() > 0 {
		d, v := b.heap.pop()
		if d > b.dist[v] {
			continue
		}
		if v == t {
			return d, true
		}
		for _, ai := range g.outArcs[g.outOff[v]:g.outOff[v+1]] {
			a := &g.arcs[ai]
			if nd := d + a.cost; nd < b.dist[a.to] {
				if b.dist[a.to] == math.MaxInt64 {
					b.order = append(b.order, a.to)
				}
				b.dist[a.to] = nd
				b.heap.push(nd, a.to)
			}
		}
	}
	return 0, false
}

// Package mcda — многокритериальная оценка: веса методом анализа иерархий (МАИ, Saaty),
// ранжирование TOPSIS (Hwang, Yoon), вклад критериев и чувствительность к весам.
package mcda

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// MaxCR — предельное отношение согласованности матрицы попарных сравнений (Saaty).
const MaxCR = 0.1

// randomIndex — случайный индекс согласованности Saaty для порядка матрицы 1…15.
var randomIndex = []float64{0, 0, 0.58, 0.90, 1.12, 1.24, 1.32, 1.41, 1.45, 1.49, 1.51, 1.48, 1.56, 1.57, 1.59}

// AHPResult — веса МАИ и согласованность матрицы.
type AHPResult struct {
	Weights   []float64
	LambdaMax float64
	// CI — индекс согласованности (λmax − n)/(n − 1); CR — CI / RI. У матриц порядка 1 и 2 CR = 0.
	CI, CR float64
}

// Consistent — CR не выше MaxCR.
func (r AHPResult) Consistent() bool { return r.CR <= MaxCR }

// ErrMatrix — матрица попарных сравнений неверна.
var ErrMatrix = errors.New("mcda: invalid pairwise matrix")

// AHP — веса критериев по матрице попарных сравнений: нормированный главный собственный вектор
// (степенной метод) и согласованность. Матрица — квадратная, положительная, обратносимметричная
// (a[j][i] = 1/a[i][j]) с единицами на диагонали; допуск 1e-6.
func AHP(m [][]float64) (AHPResult, error) {
	n := len(m)
	if n == 0 || n > len(randomIndex) {
		return AHPResult{}, fmt.Errorf("%w: order %d", ErrMatrix, n)
	}
	for i := range m {
		if len(m[i]) != n {
			return AHPResult{}, fmt.Errorf("%w: row %d has %d values", ErrMatrix, i, len(m[i]))
		}
		for j := range m[i] {
			if !(m[i][j] > 0) || math.IsInf(m[i][j], 0) {
				return AHPResult{}, fmt.Errorf("%w: a[%d][%d] = %v", ErrMatrix, i, j, m[i][j])
			}
			if math.Abs(m[i][j]*m[j][i]-1) > 1e-6 {
				return AHPResult{}, fmt.Errorf("%w: a[%d][%d] is not reciprocal", ErrMatrix, j, i)
			}
		}
	}
	w := make([]float64, n)
	for i := range w {
		w[i] = 1 / float64(n)
	}
	next := make([]float64, n)
	for range 1000 {
		var sum float64
		for i := range n {
			next[i] = 0
			for j := range n {
				next[i] += m[i][j] * w[j]
			}
			sum += next[i]
		}
		var diff float64
		for i := range n {
			next[i] /= sum
			diff = max(diff, math.Abs(next[i]-w[i]))
		}
		w, next = next, w
		if diff < 1e-12 {
			break
		}
	}
	var lambda float64
	for i := range n {
		var aw float64
		for j := range n {
			aw += m[i][j] * w[j]
		}
		lambda += aw / w[i]
	}
	lambda /= float64(n)
	r := AHPResult{Weights: w, LambdaMax: lambda}
	if n > 2 {
		r.CI = (lambda - float64(n)) / float64(n-1)
		r.CR = r.CI / randomIndex[n-1]
	}
	return r, nil
}

// Normalize делит веса на их сумму; отрицательные веса и нулевая сумма — ошибка.
func Normalize(w []float64) ([]float64, error) {
	var sum float64
	for _, v := range w {
		if v < 0 || math.IsNaN(v) {
			return nil, fmt.Errorf("mcda: negative weight %v", v)
		}
		sum += v
	}
	if sum == 0 {
		return nil, errors.New("mcda: zero weights")
	}
	out := make([]float64, len(w))
	for i, v := range w {
		out[i] = v / sum
	}
	return out, nil
}

// Criterion — критерий TOPSIS: вес и направление (Benefit — больше лучше).
type Criterion struct {
	Weight  float64
	Benefit bool
}

// Result — итог TOPSIS по альтернативам.
type Result struct {
	// Closeness — относительная близость к идеальному решению, 0…1 (больше — лучше).
	Closeness []float64
	// Contribution[i][j] — доля критерия j в оценке альтернативы i: взвешенное положение значения
	// между худшим и лучшим по критерию, нормированное на сумму по альтернативе (сумма — 1, у
	// альтернативы с худшими значениями по всем критериям — нули).
	Contribution [][]float64
}

// TOPSIS ранжирует альтернативы (строки x) по критериям (столбцы): векторная нормировка
// столбцов, взвешивание, расстояния до идеальной и антиидеальной точек. Пропуск (NaN)
// заменяется худшим значением критерия: отсутствие данных не даёт преимущества. Столбец из
// одинаковых значений не различает альтернативы и в расстояния не входит.
func TOPSIS(x [][]float64, cs []Criterion) (Result, error) {
	k := len(cs)
	if k == 0 {
		return Result{}, errors.New("mcda: no criteria")
	}
	weights := make([]float64, k)
	for j, c := range cs {
		weights[j] = c.Weight
	}
	w, err := Normalize(weights)
	if err != nil {
		return Result{}, err
	}
	n := len(x)
	v := make([][]float64, n)
	for i := range x {
		if len(x[i]) != k {
			return Result{}, fmt.Errorf("mcda: row %d has %d values, want %d", i, len(x[i]), k)
		}
		v[i] = make([]float64, k)
	}
	best, worst := make([]float64, k), make([]float64, k)
	for j, c := range cs {
		lo, hi := math.Inf(1), math.Inf(-1)
		for i := range x {
			if !math.IsNaN(x[i][j]) {
				lo, hi = min(lo, x[i][j]), max(hi, x[i][j])
			}
		}
		if math.IsInf(lo, 1) {
			lo, hi = 0, 0
		}
		fill := lo
		if !c.Benefit {
			fill = hi
		}
		var norm float64
		for i := range x {
			val := x[i][j]
			if math.IsNaN(val) {
				val = fill
			}
			v[i][j] = val
			norm += val * val
		}
		norm = math.Sqrt(norm)
		for i := range v {
			if norm > 0 {
				v[i][j] = v[i][j] / norm * w[j]
			} else {
				v[i][j] = 0
			}
		}
		if norm > 0 {
			lo, hi = lo/norm*w[j], hi/norm*w[j]
		}
		best[j], worst[j] = hi, lo
		if !c.Benefit {
			best[j], worst[j] = lo, hi
		}
	}
	res := Result{Closeness: make([]float64, n), Contribution: make([][]float64, n)}
	for i := range v {
		var dp, dm, total float64
		share := make([]float64, k)
		for j := range k {
			dp += (v[i][j] - best[j]) * (v[i][j] - best[j])
			dm += (v[i][j] - worst[j]) * (v[i][j] - worst[j])
			if span := best[j] - worst[j]; span != 0 {
				share[j] = w[j] * (v[i][j] - worst[j]) / span
				total += share[j]
			}
		}
		dp, dm = math.Sqrt(dp), math.Sqrt(dm)
		if dp+dm > 0 {
			res.Closeness[i] = dm / (dp + dm)
		}
		if total > 0 {
			for j := range share {
				share[j] /= total
			}
		}
		res.Contribution[i] = share
	}
	return res, nil
}

// Order — номера альтернатив по убыванию оценки (при равенстве — по номеру).
func Order(score []float64) []int {
	idx := make([]int, len(score))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		switch {
		case score[a] > score[b]:
			return -1
		case score[a] < score[b]:
			return 1
		}
		return a - b
	})
	return idx
}

// Sensitivity — устойчивость ранжирования к изменению веса одного критерия.
type Sensitivity struct {
	Criterion int
	// Change — относительное изменение веса (−0,2 — на 20 % меньше), остальные веса
	// перенормируются.
	Change float64
	// Spearman — ранговая корреляция с исходным ранжированием; TopKept — доля исходных первых top
	// альтернатив, оставшихся в первых top.
	Spearman float64
	TopKept  float64
}

// Sensitivities пересчитывает TOPSIS при изменении веса каждого критерия на ±delta.
func Sensitivities(x [][]float64, cs []Criterion, delta float64, top int) ([]Sensitivity, error) {
	base, err := TOPSIS(x, cs)
	if err != nil {
		return nil, err
	}
	baseTop := topSet(base.Closeness, top)
	var out []Sensitivity
	for j := range cs {
		for _, ch := range []float64{-delta, delta} {
			alt := slices.Clone(cs)
			alt[j].Weight *= 1 + ch
			r, err := TOPSIS(x, alt)
			if err != nil {
				return nil, err
			}
			kept := 0
			for i := range topSet(r.Closeness, top) {
				if baseTop[i] {
					kept++
				}
			}
			s := Sensitivity{Criterion: j, Change: ch, Spearman: Spearman(base.Closeness, r.Closeness)}
			if len(baseTop) > 0 {
				s.TopKept = float64(kept) / float64(len(baseTop))
			}
			out = append(out, s)
		}
	}
	return out, nil
}

func topSet(score []float64, top int) map[int]bool {
	order := Order(score)
	out := make(map[int]bool, top)
	for _, i := range order[:min(top, len(order))] {
		out[i] = true
	}
	return out
}

// Spearman — ранговая корреляция Спирмена (средние ранги для равных значений).
func Spearman(a, b []float64) float64 {
	ra, rb := ranks(a), ranks(b)
	n := float64(len(a))
	if n < 2 {
		return 1
	}
	var ma, mb float64
	for i := range ra {
		ma += ra[i]
		mb += rb[i]
	}
	ma, mb = ma/n, mb/n
	var cov, va, vb float64
	for i := range ra {
		cov += (ra[i] - ma) * (rb[i] - mb)
		va += (ra[i] - ma) * (ra[i] - ma)
		vb += (rb[i] - mb) * (rb[i] - mb)
	}
	if va == 0 || vb == 0 {
		return 1
	}
	return cov / math.Sqrt(va*vb)
}

func ranks(v []float64) []float64 {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		switch {
		case v[a] < v[b]:
			return -1
		case v[a] > v[b]:
			return 1
		}
		return 0
	})
	out := make([]float64, len(v))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && v[idx[j+1]] == v[idx[i]] {
			j++
		}
		r := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[idx[k]] = r
		}
		i = j + 1
	}
	return out
}

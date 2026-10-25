// Package forecast — прогноз рядов показателей: сезонная наивная модель, Холт–Уинтерс,
// регрессия с календарными признаками; выбор модели по MASE на скользящей проверке, интервалы —
// бутстреп ошибок.
package forecast

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"gonum.org/v1/gonum/mat"
)

// ErrShort — ряд слишком короткий для проверки моделей.
var ErrShort = errors.New("forecast: series too short")

// Series — ряд с сезонностью Season (12 — месяцы, 1 — без сезонности). Exog — внешние признаки
// по точкам ряда и Future — по точкам горизонта (например, число рабочих дней в месяце);
// Months — номер месяца 1–12 каждой точки ряда и горизонта (для регрессии).
type Series struct {
	Values []float64
	Season int
	Exog   [][]float64
	Future [][]float64
	Months []int
	// NonNegative — значения не бывают отрицательными (счётчики): прогноз и интервалы не ниже 0.
	NonNegative bool
}

// model — модель прогноза: по началу ряда train (первые n точек) — значения на h шагов вперёд.
type model struct {
	name string
	// fit возвращает прогноз на h шагов по первым n точкам ряда.
	fit func(s Series, n, h int) ([]float64, error)
}

func models(s Series) []model {
	out := []model{{name: "seasonal_naive", fit: seasonalNaive}, {name: "holt_winters", fit: holtWinters}}
	if len(s.Months) > 0 {
		out = append(out, model{name: "regression", fit: regression})
	}
	return out
}

// seasonalNaive — значение того же сезона прошлого цикла (без сезонности — последнее значение).
func seasonalNaive(s Series, n, h int) ([]float64, error) {
	m := max(s.Season, 1)
	if n < m {
		return nil, ErrShort
	}
	out := make([]float64, h)
	for i := range h {
		out[i] = s.Values[n-m+i%m]
	}
	return out, nil
}

// holtWinters — аддитивная модель уровня, тренда и сезона; параметры подбираются по сетке по
// сумме квадратов одношаговых ошибок. Короче двух сезонов — модель Хольта без сезона.
func holtWinters(s Series, n, h int) ([]float64, error) {
	y := s.Values[:n]
	m := s.Season
	seasonal := m > 1 && n >= 2*m
	if n < 4 {
		return nil, ErrShort
	}
	grid := []float64{0.05, 0.1, 0.2, 0.3, 0.5, 0.7, 0.9}
	gammas := []float64{0}
	if seasonal {
		gammas = grid
	}
	bestSSE := math.Inf(1)
	var best []float64
	for _, a := range grid {
		for _, b := range grid {
			for _, g := range gammas {
				sse, fc := hwRun(y, m, seasonal, a, b, g, h)
				if sse < bestSSE {
					bestSSE, best = sse, fc
				}
			}
		}
	}
	return best, nil
}

// hwRun — одна прогонка Холта–Уинтерса: сумма квадратов одношаговых ошибок и прогноз на h шагов.
func hwRun(y []float64, m int, seasonal bool, alpha, beta, gamma float64, h int) (float64, []float64) {
	var level, trend float64
	season := make([]float64, max(m, 1))
	start := 1
	if seasonal {
		// Начальные значения: средние двух первых сезонов, сезонность — отклонения первого.
		var s1, s2 float64
		for i := range m {
			s1 += y[i]
			s2 += y[m+i]
		}
		s1, s2 = s1/float64(m), s2/float64(m)
		level, trend = s1, (s2-s1)/float64(m)
		for i := range m {
			season[i] = y[i] - s1
		}
		start = m
	} else {
		level, trend = y[0], y[1]-y[0]
	}
	var sse float64
	for t := start; t < len(y); t++ {
		si := 0.0
		if seasonal {
			si = season[t%m]
		}
		pred := level + trend + si
		e := y[t] - pred
		sse += e * e
		prevLevel := level
		level = alpha*(y[t]-si) + (1-alpha)*(level+trend)
		trend = beta*(level-prevLevel) + (1-beta)*trend
		if seasonal {
			season[t%m] = gamma*(y[t]-level) + (1-gamma)*si
		}
	}
	fc := make([]float64, h)
	for i := range h {
		si := 0.0
		if seasonal {
			si = season[(len(y)+i)%m]
		}
		fc[i] = level + float64(i+1)*trend + si
	}
	return sse, fc
}

// regression — линейная регрессия: константа, тренд, месяц (11 фиктивных признаков при
// месячном ряде) и внешние признаки; метод наименьших квадратов (QR).
func regression(s Series, n, h int) ([]float64, error) {
	row := func(t int) []float64 {
		r := []float64{1, float64(t)}
		if s.Season == 12 {
			for k := 2; k <= 12; k++ {
				if s.Months[t] == k {
					r = append(r, 1)
				} else {
					r = append(r, 0)
				}
			}
		}
		var ex []float64
		switch {
		case t < len(s.Values) && len(s.Exog) > t:
			ex = s.Exog[t]
		case t >= len(s.Values) && len(s.Future) > t-len(s.Values):
			ex = s.Future[t-len(s.Values)]
		}
		return append(r, ex...)
	}
	cols := len(row(0))
	if n < cols+4 || len(s.Months) < n+h {
		return nil, ErrShort
	}
	x := mat.NewDense(n, cols, nil)
	yv := mat.NewVecDense(n, nil)
	for t := range n {
		x.SetRow(t, row(t))
		yv.SetVec(t, s.Values[t])
	}
	var qr mat.QR
	qr.Factorize(x)
	var beta mat.VecDense
	if err := qr.SolveVecTo(&beta, false, yv); err != nil {
		// Внешний признак выражается через месяцы (вырожденная матрица) — без него.
		if len(s.Exog) > 0 {
			s.Exog, s.Future = nil, nil
			return regression(s, n, h)
		}
		return nil, fmt.Errorf("regression: %w", err)
	}
	out := make([]float64, h)
	for i := range h {
		r := row(n + i)
		if len(r) != cols {
			return nil, ErrShort
		}
		out[i] = mat.Dot(mat.NewVecDense(cols, r), &beta)
	}
	return out, nil
}

// Score — точность модели на скользящей проверке.
type Score struct {
	Model string
	MASE  float64
	// Points — пар (начало, шаг), на которых проверена модель.
	Points int
}

// Point — прогноз на шаг горизонта с интервалами 80 и 95 %.
type Point struct {
	Value      float64
	Lo80, Hi80 float64
	Lo95, Hi95 float64
}

// Result — выбранная модель, точность всех моделей и прогноз.
type Result struct {
	Model    string
	Scores   []Score
	Forecast []Point
	// Holdout — начал скользящей проверки.
	Holdout int
}

// Forecast проверяет модели скользящим началом на последних точках ряда (не больше 12 начал,
// не больше трети ряда), выбирает модель с наименьшим MASE и строит прогноз на h шагов.
// MASE — средняя абсолютная ошибка, делённая на среднюю ошибку сезонной наивной модели на
// одном шаге внутри ряда.
func Forecast(s Series, h int, seed uint64) (*Result, error) {
	n := len(s.Values)
	m := max(s.Season, 1)
	origins := min(12, n/3)
	if origins < 3 || n-origins < 2*m+1 {
		return nil, ErrShort
	}
	var scale float64
	for t := m; t < n; t++ {
		scale += math.Abs(s.Values[t] - s.Values[t-m])
	}
	scale /= float64(n - m)
	if scale == 0 {
		scale = 1
	}
	res := &Result{Holdout: origins}
	errs := map[string][]float64{} // одношаговые ошибки выбранной модели
	bestMASE := math.Inf(1)
	for _, md := range models(s) {
		var sum float64
		var points int
		var one []float64
		for o := n - origins; o < n; o++ {
			fc, err := md.fit(s, o, min(h, n-o))
			if err != nil {
				points = 0
				break
			}
			for i, v := range fc {
				sum += math.Abs(s.Values[o+i] - v)
				points++
				if i == 0 {
					one = append(one, s.Values[o]-v)
				}
			}
		}
		if points == 0 {
			continue
		}
		sc := Score{Model: md.name, MASE: sum / float64(points) / scale, Points: points}
		res.Scores = append(res.Scores, sc)
		errs[md.name] = one
		if sc.MASE < bestMASE {
			bestMASE, res.Model = sc.MASE, md.name
		}
	}
	if res.Model == "" {
		return nil, ErrShort
	}
	var chosen model
	for _, md := range models(s) {
		if md.name == res.Model {
			chosen = md
		}
	}
	fc, err := chosen.fit(s, n, h)
	if err != nil {
		return nil, err
	}
	res.Forecast = intervals(fc, errs[res.Model], s.NonNegative, seed)
	return res, nil
}

// intervals — бутстреп: ошибка на шаге h — сумма h одношаговых ошибок скользящей проверки,
// выбранных с возвращением; 2000 выборок, квантили 10/90 и 2,5/97,5 %.
func intervals(fc, one []float64, nonNeg bool, seed uint64) []Point {
	const samples = 2000
	// Воспроизводимый генератор с заданным зерном: криптостойкость не нужна.
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // бутстреп, не секреты
	out := make([]Point, len(fc))
	acc := make([]float64, samples)
	for h, v := range fc {
		for b := range samples {
			if len(one) > 0 {
				acc[b] += one[rng.IntN(len(one))]
			}
		}
		sorted := slices.Clone(acc)
		slices.Sort(sorted)
		q := func(p float64) float64 { return v + sorted[int(p*float64(samples-1))] }
		p := Point{Value: v, Lo80: q(0.1), Hi80: q(0.9), Lo95: q(0.025), Hi95: q(0.975)}
		if nonNeg {
			p.Value, p.Lo80, p.Lo95 = math.Max(0, p.Value), math.Max(0, p.Lo80), math.Max(0, p.Lo95)
			p.Hi80, p.Hi95 = math.Max(0, p.Hi80), math.Max(0, p.Hi95)
		}
		out[h] = p
	}
	return out
}

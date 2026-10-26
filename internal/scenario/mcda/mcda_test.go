package mcda

import (
	"errors"
	"math"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f", name, got, want)
	}
}

// Пример из учебников по МАИ (Saaty): три критерия, «умеренно» и «сильно» предпочтительнее.
func TestAHPTextbook(t *testing.T) {
	t.Parallel()
	r, err := AHP([][]float64{
		{1, 3, 5},
		{1.0 / 3, 1, 3},
		{1.0 / 5, 1.0 / 3, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{0.637, 0.258, 0.105} {
		near(t, "w", r.Weights[i], want, 0.001)
	}
	near(t, "λmax", r.LambdaMax, 3.039, 0.001)
	near(t, "CR", r.CR, 0.033, 0.001)
	if !r.Consistent() {
		t.Error("matrix should be consistent")
	}
}

// Согласованная матрица (a_ij = w_i / w_j) восстанавливает веса точно, CR = 0.
func TestAHPConsistentMatrix(t *testing.T) {
	t.Parallel()
	w := []float64{0.4, 0.3, 0.2, 0.1}
	m := make([][]float64, len(w))
	for i := range w {
		m[i] = make([]float64, len(w))
		for j := range w {
			m[i][j] = w[i] / w[j]
		}
	}
	r, err := AHP(m)
	if err != nil {
		t.Fatal(err)
	}
	for i := range w {
		near(t, "w", r.Weights[i], w[i], 1e-9)
	}
	near(t, "CR", r.CR, 0, 1e-9)
}

func TestAHPInconsistentAndInvalid(t *testing.T) {
	t.Parallel()
	// A сильно лучше B, B сильно лучше C, но C сильно лучше A.
	r, err := AHP([][]float64{{1, 7, 1.0 / 7}, {1.0 / 7, 1, 7}, {7, 1.0 / 7, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent() {
		t.Errorf("CR = %.2f, cyclic preferences must be inconsistent", r.CR)
	}
	for _, m := range [][][]float64{
		{},
		{{1, 2}, {2, 1}},
		{{1, 0}, {0, 1}},
		{{1, 2, 3}, {0.5, 1}},
	} {
		if _, err := AHP(m); !errors.Is(err, ErrMatrix) {
			t.Errorf("AHP(%v) err = %v, want ErrMatrix", m, err)
		}
	}
}

// Пример Hwang, Yoon (1981): выбор истребителя, 4 альтернативы, 6 критериев (4-й — затратный).
func TestTOPSISHwangYoon(t *testing.T) {
	t.Parallel()
	x := [][]float64{
		{2.0, 1500, 20000, 5.5, 5, 9},
		{2.5, 2700, 18000, 6.5, 3, 5},
		{1.8, 2000, 21000, 4.5, 7, 7},
		{2.2, 1800, 20000, 5.0, 5, 5},
	}
	cs := []Criterion{{0.2, true}, {0.1, true}, {0.1, true}, {0.1, false}, {0.2, true}, {0.3, true}}
	r, err := TOPSIS(x, cs)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{0.643, 0.268, 0.613, 0.312} {
		near(t, "C", r.Closeness[i], want, 0.001)
	}
	order := Order(r.Closeness)
	for i, want := range []int{0, 2, 3, 1} {
		if order[i] != want {
			t.Fatalf("order = %v, want [0 2 3 1]", order)
		}
	}
	for _, c := range r.Contribution {
		var sum float64
		for _, v := range c {
			sum += v
		}
		near(t, "Σ contribution", sum, 1, 1e-9)
	}
}

func TestTOPSISMissingAndConstant(t *testing.T) {
	t.Parallel()
	// Второй критерий одинаков у всех и на оценку не влияет; пропуск — худшее значение.
	x := [][]float64{{10, 5}, {math.NaN(), 5}, {0, 5}}
	r, err := TOPSIS(x, []Criterion{{1, true}, {1, true}})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "best", r.Closeness[0], 1, 1e-9)
	near(t, "missing", r.Closeness[1], 0, 1e-9)
	near(t, "worst", r.Closeness[2], 0, 1e-9)
	near(t, "constant share", r.Contribution[0][1], 0, 1e-9)
	if _, err := TOPSIS(x, []Criterion{{0, true}, {0, false}}); err == nil {
		t.Error("zero weights must fail")
	}
}

func TestSensitivities(t *testing.T) {
	t.Parallel()
	x := [][]float64{{1, 9}, {2, 8}, {3, 7}, {4, 6}, {5, 5}}
	s, err := Sensitivities(x, []Criterion{{0.9, true}, {0.1, true}}, 0.2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 4 {
		t.Fatalf("len = %d, want 4", len(s))
	}
	for _, v := range s {
		if v.Spearman < 0.99 || v.TopKept != 1 {
			t.Errorf("%+v: dominant criterion, ranking must be stable", v)
		}
	}
	near(t, "spearman reversed", Spearman([]float64{1, 2, 3}, []float64{3, 2, 1}), -1, 1e-9)
	near(t, "spearman ties", Spearman([]float64{1, 1, 2}, []float64{1, 1, 2}), 1, 1e-9)
}

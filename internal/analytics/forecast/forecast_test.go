package forecast

import (
	"errors"
	"math"
	"testing"
)

// airPassengers — пассажиры международных авиалиний, тыс. в месяц, 1949–1960 (Box, Jenkins,
// «Time Series Analysis», серия G) — эталонный сезонный ряд с трендом.
var airPassengers = []float64{
	112, 118, 132, 129, 121, 135, 148, 148, 136, 119, 104, 118,
	115, 126, 141, 135, 125, 149, 170, 170, 158, 133, 114, 140,
	145, 150, 178, 163, 172, 178, 199, 199, 184, 162, 146, 166,
	171, 180, 193, 181, 183, 218, 230, 242, 209, 191, 172, 194,
	196, 196, 236, 235, 229, 243, 264, 272, 237, 211, 180, 201,
	204, 188, 235, 227, 234, 264, 302, 293, 259, 229, 203, 229,
	242, 233, 267, 269, 270, 315, 364, 347, 312, 274, 237, 278,
	284, 277, 317, 313, 318, 374, 413, 405, 355, 306, 271, 306,
	315, 301, 356, 348, 355, 422, 465, 467, 404, 347, 305, 336,
	340, 318, 362, 348, 363, 435, 491, 505, 404, 359, 310, 337,
	360, 342, 406, 396, 420, 472, 548, 559, 463, 407, 362, 405,
	417, 391, 419, 461, 472, 535, 622, 606, 508, 461, 390, 432,
}

func months(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i%12 + 1
	}
	return out
}

func score(r *Result, model string) Score {
	for _, s := range r.Scores {
		if s.Model == model {
			return s
		}
	}
	return Score{MASE: math.NaN()}
}

func TestAirPassengers(t *testing.T) {
	t.Parallel()
	s := Series{Values: airPassengers, Season: 12, Months: months(len(airPassengers) + 12), NonNegative: true}
	r, err := Forecast(s, 12, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Ряд с трендом: Холт–Уинтерс и регрессия точнее сезонной наивной модели.
	naive, hw := score(r, "seasonal_naive"), score(r, "holt_winters")
	if !(hw.MASE < naive.MASE) || r.Model == "seasonal_naive" {
		t.Errorf("выбор модели %s: %+v", r.Model, r.Scores)
	}
	// Прогноз 1961 года — выше 1960-го в среднем и с тем же пиком в июле — августе.
	var mean float64
	for _, p := range r.Forecast {
		mean += p.Value / 12
		if !(p.Lo95 <= p.Lo80 && p.Lo80 <= p.Value && p.Value <= p.Hi80 && p.Hi80 <= p.Hi95) {
			t.Fatalf("интервалы не вложены: %+v", p)
		}
	}
	if mean < 450 || mean > 560 {
		t.Errorf("средний прогноз %v", mean)
	}
	if r.Forecast[6].Value < r.Forecast[10].Value {
		t.Errorf("сезон: июль %v < ноябрь %v", r.Forecast[6].Value, r.Forecast[10].Value)
	}
	// Интервалы расширяются с горизонтом.
	if r.Forecast[11].Hi95-r.Forecast[11].Lo95 <= r.Forecast[0].Hi95-r.Forecast[0].Lo95 {
		t.Errorf("интервал не растёт с горизонтом")
	}
}

func TestExactModels(t *testing.T) {
	t.Parallel()
	// Чистая сезонность — сезонная наивная модель точна.
	pure := make([]float64, 60)
	for i := range pure {
		pure[i] = []float64{10, 20, 30, 40, 50, 60, 50, 40, 30, 20, 10, 5}[i%12]
	}
	fc, err := seasonalNaive(Series{Values: pure, Season: 12}, 60, 12)
	if err != nil || fc[0] != 10 || fc[5] != 60 {
		t.Errorf("сезонная наивная: %v %v", fc, err)
	}
	// Тренд + сезон + внешний признак — регрессия восстанавливает точно.
	n := 72
	vals := make([]float64, n)
	exog := make([][]float64, n)
	future := make([][]float64, 12)
	for i := range n + 12 {
		wd := float64(20 + (i*7)%5)
		v := 100 + 2*float64(i) + 5*wd + float64(i%12)
		if i < n {
			vals[i], exog[i] = v, []float64{wd}
		} else {
			future[i-n] = []float64{wd}
		}
	}
	s := Series{Values: vals, Season: 12, Months: months(n + 12), Exog: exog, Future: future}
	fc, err = regression(s, n, 12)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range fc {
		wd := float64(20 + ((n+i)*7)%5)
		want := 100 + 2*float64(n+i) + 5*wd + float64((n+i)%12)
		if math.Abs(v-want) > 1e-6 {
			t.Fatalf("регрессия, шаг %d: %v, ожидалось %v", i, v, want)
		}
	}
	r, err := Forecast(s, 6, 1)
	if err != nil || r.Model != "regression" || score(r, "regression").MASE > 1e-6 {
		t.Errorf("выбор регрессии: %+v %v", r, err)
	}
}

func TestShortAndYearly(t *testing.T) {
	t.Parallel()
	if _, err := Forecast(Series{Values: []float64{1, 2, 3, 4, 5}, Season: 12}, 3, 1); !errors.Is(err, ErrShort) {
		t.Errorf("короткий ряд: %v", err)
	}
	// Годовой ряд без сезонности: модель Хольта, прогноз продолжает тренд.
	yearly := []float64{300, 310, 318, 330, 338, 351, 360, 368, 381, 390, 401, 409, 420, 431, 440}
	r, err := Forecast(Series{Values: yearly, Season: 1, NonNegative: true}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Forecast[0].Value < 440 || r.Forecast[2].Value < r.Forecast[0].Value {
		t.Errorf("тренд: %+v (%s)", r.Forecast, r.Model)
	}
	// Счётчики: прогноз и интервалы не ниже нуля.
	low := []float64{3, 0, 1, 0, 2, 0, 0, 1, 0, 0, 1, 0, 2, 0, 1, 0, 0, 1, 0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 1, 0}
	r, err = Forecast(Series{Values: low, Season: 1, NonNegative: true}, 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range r.Forecast {
		if p.Lo95 < 0 || p.Value < 0 {
			t.Fatalf("отрицательный прогноз счётчика %+v", p)
		}
	}
}

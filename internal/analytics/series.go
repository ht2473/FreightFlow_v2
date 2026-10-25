package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"freightflow/internal/analytics/forecast"
)

// Периодичность рядов.
const (
	SeriesMonth = "month"
	SeriesYear  = "year"
)

// SeriesPoint — значение ряда за период; Filled — периода нет в источнике (у потока — ноль).
type SeriesPoint struct {
	Period time.Time
	Value  float64
	Filled bool
}

// ErrSparse — в ряду потока больше трети периодов без данных: нули вместо них исказили бы прогноз.
var ErrSparse = errors.New("analytics: sparse series")

// Series — ряд показателя по городу: периоды подряд; у потоков пропуск — ноль, неполный текущий
// период не берётся (у остальных пропуски остаются пропусками — прогноз по такому ряду не строится).
func (s *Service) Series(ctx context.Context, code string) (Metric, []SeriesPoint, error) {
	m, ok := Find(code)
	if !ok || m.Series == "" {
		return m, nil, fmt.Errorf("%w: ряд %s", ErrUnknown, code)
	}
	rows, err := s.pool.Query(ctx, `SELECT period, value FROM analytics.metric_value
		WHERE metric_code = $1 AND level = 'city' AND period IS NOT NULL ORDER BY period`, code)
	if err != nil {
		return m, nil, err
	}
	defer rows.Close()
	got := map[time.Time]float64{}
	var first, last time.Time
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.Period, &p.Value); err != nil {
			return m, nil, err
		}
		if first.IsZero() {
			first = p.Period
		}
		last = p.Period
		got[p.Period] = p.Value
	}
	if err := rows.Err(); err != nil || first.IsZero() {
		return m, nil, err
	}
	step := func(t time.Time) time.Time {
		if m.Series == SeriesYear {
			return t.AddDate(1, 0, 0)
		}
		return t.AddDate(0, 1, 0)
	}
	now := time.Now()
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if m.Series == SeriesYear {
		current = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
	var out []SeriesPoint
	for t := first; !t.After(last); t = step(t) {
		if m.Flow && !t.Before(current) {
			break
		}
		v, ok := got[t]
		if !ok && !m.Flow {
			continue
		}
		out = append(out, SeriesPoint{Period: t, Value: v, Filled: !ok})
	}
	return m, out, nil
}

// Forecast — прогноз ряда показателя на h периодов: модели, точность, интервалы. Для месячных
// рядов внешний признак — число рабочих дней месяца (производственный календарь), если он
// известен на весь горизонт.
func (s *Service) Forecast(ctx context.Context, code string, h int) (Metric, []SeriesPoint, *forecast.Result, error) {
	m, pts, err := s.Series(ctx, code)
	if err != nil {
		return m, nil, nil, err
	}
	// Пропуски в ряду (не поток) — прогноз не строится.
	if len(pts) > 1 && !m.Flow {
		for i := 1; i < len(pts); i++ {
			want := pts[i-1].Period.AddDate(0, 1, 0)
			if m.Series == SeriesYear {
				want = pts[i-1].Period.AddDate(1, 0, 0)
			}
			if !pts[i].Period.Equal(want) {
				return m, pts, nil, fmt.Errorf("%w: пропуск в ряду перед %s", forecast.ErrShort, pts[i].Period.Format("01.2006"))
			}
		}
	}
	filled := 0
	for _, p := range pts {
		if p.Filled {
			filled++
		}
	}
	if filled*3 > len(pts) {
		return m, pts, nil, fmt.Errorf("%w: %d из %d периодов без данных", ErrSparse, filled, len(pts))
	}
	series := forecast.Series{Values: make([]float64, len(pts)), Season: 1, NonNegative: true}
	for i, p := range pts {
		series.Values[i] = p.Value
	}
	if m.Series == SeriesMonth && len(pts) > 0 {
		series.Season = 12
		periods := make([]time.Time, 0, len(pts)+h)
		for _, p := range pts {
			periods = append(periods, p.Period)
		}
		for i := 1; i <= h; i++ {
			periods = append(periods, pts[len(pts)-1].Period.AddDate(0, i, 0))
		}
		for _, p := range periods {
			series.Months = append(series.Months, int(p.Month()))
		}
		days, err := s.workdays(ctx, periods[0], periods[len(periods)-1])
		if err != nil {
			return m, pts, nil, err
		}
		complete := true
		var exog [][]float64
		for _, p := range periods {
			d, ok := days[p]
			if !ok {
				complete = false
				break
			}
			exog = append(exog, []float64{d})
		}
		if complete {
			series.Exog, series.Future = exog[:len(pts)], exog[len(pts):]
		}
	}
	res, err := forecast.Forecast(series, h, 1)
	return m, pts, res, err
}

// workdays — число рабочих дней (с сокращёнными) по месяцам календаря.
func (s *Service) workdays(ctx context.Context, from, to time.Time) (map[time.Time]float64, error) {
	rows, err := s.pool.Query(ctx, `SELECT date_trunc('month', day)::date, count(*) FILTER (WHERE kind IN ('workday', 'short'))::float8,
		       count(*) FROM ref.calendar_day WHERE day >= $1 AND day < ($2::date + interval '1 month')
		GROUP BY 1`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[time.Time]float64{}
	for rows.Next() {
		var m time.Time
		var work float64
		var days int
		if err := rows.Scan(&m, &work, &days); err != nil {
			return nil, err
		}
		// Месяц календаря неполный — признак неизвестен.
		if days >= 28 {
			out[m] = work
		}
	}
	return out, rows.Err()
}

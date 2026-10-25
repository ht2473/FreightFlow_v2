package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/go-faster/jx"

	"freightflow/internal/analytics"
	"freightflow/internal/analytics/forecast"
	"freightflow/internal/api/gen"
)

// --- Показатели ---

func metricDef(d analytics.Def) gen.MetricDef {
	out := gen.MetricDef{
		Code: d.Code, Title: d.Title, Unit: d.Unit, Formula: d.Formula, Method: d.Method, Domains: nonNil(d.Domains),
		DependsOn: nonNil(d.DependsOn), Version: d.Version, Rows: d.Rows,
	}
	for _, l := range d.Levels {
		out.Levels = append(out.Levels, gen.MetricDefLevelsItem(l))
	}
	if d.ComputedAt != nil {
		out.ComputedAt = gen.NewOptDateTime(*d.ComputedAt)
	}
	return out
}

func metricValue(v analytics.Value) (gen.MetricValue, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(v.Inputs, &fields); err != nil {
		return gen.MetricValue{}, err
	}
	inputs := gen.MetricValueInputs{}
	for k, raw := range fields {
		inputs[k] = jx.Raw(raw)
	}
	out := gen.MetricValue{
		Code: v.Code, Level: gen.MetricValueLevel(v.Level), Entity: v.Entity, Label: v.Label, Value: v.Value,
		Inputs: inputs, Rank: v.Rank, Of: v.Of,
	}
	if v.Period != nil {
		out.Period = gen.NewOptDate(*v.Period)
	}
	return out, nil
}

func metricValues(list []analytics.Value) ([]gen.MetricValue, error) {
	out := make([]gen.MetricValue, len(list))
	for i, v := range list {
		var err error
		if out[i], err = metricValue(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Server) ListMetrics(ctx context.Context) ([]gen.MetricDef, error) {
	defs, err := s.analytics.Defs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.MetricDef, len(defs))
	for i, d := range defs {
		out[i] = metricDef(d)
	}
	return out, nil
}

func (s *Server) MetricValues(ctx context.Context, p gen.MetricValuesParams) (*gen.MetricValues, error) {
	defs, err := s.analytics.Defs(ctx)
	if err != nil {
		return nil, err
	}
	var def *analytics.Def
	for i := range defs {
		if defs[i].Code == p.Code {
			def = &defs[i]
		}
	}
	if def == nil {
		return nil, analytics.ErrUnknown
	}
	list, err := s.analytics.Values(ctx, p.Code, string(p.Level.Or("")), p.Series.Or(false), p.Limit.Or(500))
	if err != nil {
		return nil, err
	}
	values, err := metricValues(list)
	if err != nil {
		return nil, err
	}
	return &gen.MetricValues{Metric: metricDef(*def), Values: values}, nil
}

func (s *Server) EntityMetrics(ctx context.Context, p gen.EntityMetricsParams) ([]gen.MetricValue, error) {
	list, err := s.analytics.EntityValues(ctx, string(p.Level), p.Entity)
	if err != nil {
		return nil, err
	}
	return metricValues(list)
}

func (s *Server) MetricSeries(ctx context.Context, p gen.MetricSeriesParams) (*gen.MetricSeries, error) {
	h := p.Horizon.Or(12)
	m, pts, res, err := s.analytics.Forecast(ctx, p.Code, h)
	note := ""
	switch {
	case errors.Is(err, analytics.ErrSparse):
		note = "Больше трети периодов ряда без данных в источнике — прогноз по нему был бы недостоверен."
	case errors.Is(err, forecast.ErrShort):
		note = "Ряд короткий или с пропусками — прогноз не строится."
	case err != nil:
		return nil, err
	}
	defs, err := s.analytics.Defs(ctx)
	if err != nil {
		return nil, err
	}
	out := &gen.MetricSeries{Points: make([]gen.MetricSeriesPointsItem, len(pts))}
	for _, d := range defs {
		if d.Code == m.Code {
			out.Metric = metricDef(d)
		}
	}
	for i, pt := range pts {
		out.Points[i] = gen.MetricSeriesPointsItem{Period: pt.Period, Value: pt.Value, Filled: pt.Filled}
	}
	if note != "" {
		out.ForecastNote = gen.NewOptString(note)
		return out, nil
	}
	fc := gen.MetricSeriesForecast{
		Model: gen.MetricSeriesForecastModel(res.Model), Holdout: res.Holdout,
		Points: make([]gen.MetricSeriesForecastPointsItem, len(res.Forecast)),
	}
	for _, sc := range res.Scores {
		fc.Scores = append(fc.Scores, gen.MetricSeriesForecastScoresItem{Model: sc.Model, Mase: sc.MASE, Points: sc.Points})
	}
	last := pts[len(pts)-1].Period
	for i, fp := range res.Forecast {
		period := last.AddDate(0, i+1, 0)
		if m.Series == analytics.SeriesYear {
			period = last.AddDate(i+1, 0, 0)
		}
		fc.Points[i] = gen.MetricSeriesForecastPointsItem{
			Period: period, Value: fp.Value, Lo80: fp.Lo80, Hi80: fp.Hi80, Lo95: fp.Lo95, Hi95: fp.Hi95,
		}
	}
	out.Forecast = gen.NewOptMetricSeriesForecast(fc)
	return out, nil
}

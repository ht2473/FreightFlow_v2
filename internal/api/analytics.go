package api

import (
	"context"
	"encoding/json"

	"github.com/go-faster/jx"

	"freightflow/internal/analytics"
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

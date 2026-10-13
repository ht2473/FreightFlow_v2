package api

import (
	"context"
	"errors"
	"time"

	"freightflow/internal/api/gen"
	"freightflow/internal/routing"
)

// --- Маршрутизация ---

func (s *Server) ListVehicleProfiles(ctx context.Context) ([]gen.VehicleProfile, error) {
	list, err := s.routing.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.VehicleProfile, len(list))
	for i, p := range list {
		out[i] = gen.VehicleProfile{
			Code: p.Code, Name: p.Name, GrossT: p.GrossT, PayloadT: p.PayloadT, AxleLoadT: p.AxleLoadT,
			HeightM: p.HeightM, WidthM: p.WidthM, LengthM: p.LengthM, EcoClass: p.EcoClass, Hazmat: p.Hazmat,
		}
	}
	return out, nil
}

func (s *Server) BuildRoute(ctx context.Context, req *gen.RouteRequest) (*gen.RouteResult, error) {
	v := req.Vehicle
	in := routing.RouteInput{
		Profile: routing.Profile{
			GrossT: v.GrossT, PayloadT: v.PayloadT.Or(0), AxleLoadT: v.AxleLoadT.Or(0), HeightM: v.HeightM,
			WidthM: v.WidthM, LengthM: v.LengthM, EcoClass: v.EcoClass.Or(0), Hazmat: v.Hazmat.Or(false),
		},
		Alternates: req.Alternates.Or(0),
	}
	for _, p := range req.Points {
		in.Points = append(in.Points, routing.Point{Lon: p.Lon, Lat: p.Lat})
	}
	for _, p := range req.Passes {
		in.Passes = append(in.Passes, routing.Pass{Zone: string(p.Zone), Kind: string(p.Kind)})
	}
	if t, ok := req.Depart.Get(); ok {
		in.Depart = t
	} else {
		in.Depart = time.Now()
	}
	res, err := s.routing.Route(ctx, in)
	if err != nil && !errors.Is(err, routing.ErrBlocked) {
		return nil, err
	}
	out := &gen.RouteResult{
		Routes:       make([]gen.RouteLine, len(res.Routes)),
		Zones:        make([]gen.ZoneVerdict, len(res.Zones)),
		Blocked:      make([]gen.BlockedPoint, len(res.Blocked)),
		Avoided:      make([]gen.AvoidedRestriction, len(res.Avoided)),
		AvoidedZones: make([]gen.RouteResultAvoidedZonesItem, len(res.AvoidedZones)),
	}
	for i, r := range res.Routes {
		out.Routes[i] = toRouteLine(r)
	}
	if res.Baseline != nil {
		out.Baseline = gen.NewOptRouteLine(toRouteLine(*res.Baseline))
	}
	for i, d := range res.Zones {
		z := gen.ZoneVerdict{
			Zone: gen.ZoneVerdictZone(d.Zone), Forbidden: d.Forbidden, Unconfirmed: d.Unconfirmed,
			Rules: make([]gen.ZoneRuleUse, len(d.Rules)),
		}
		for j, a := range d.Rules {
			u := gen.ZoneRuleUse{Code: a.Rule.Code, Title: a.Rule.Title, Blocking: a.Blocking, Confirmed: a.Rule.Confirmed}
			basis := a.Rule.Document
			if a.Rule.Clause != "" {
				basis += ", " + a.Rule.Clause
			}
			u.Basis = gen.NewOptString(basis)
			if a.Note != "" {
				u.Note = gen.NewOptZoneRuleUseNote(gen.ZoneRuleUseNote(a.Note))
			}
			z.Rules[j] = u
		}
		out.Zones[i] = z
	}
	for i, b := range res.Blocked {
		out.Blocked[i] = gen.BlockedPoint{Index: b.Index, Zone: gen.BlockedPointZone(b.Zone)}
	}
	for i, a := range res.Avoided {
		r := gen.AvoidedRestriction{Kind: a.Kind, Street: a.Street, Origins: a.Origins, Segments: a.Segments, LengthM: a.LengthM}
		if a.Value != nil {
			r.Value = gen.NewOptFloat64(*a.Value)
		}
		out.Avoided[i] = r
	}
	for i, z := range res.AvoidedZones {
		out.AvoidedZones[i] = gen.RouteResultAvoidedZonesItem(z)
	}
	return out, nil
}

func toRouteLine(r routing.Route) gen.RouteLine {
	out := gen.RouteLine{TimeS: r.Time, LengthKm: r.Length, Coordinates: [][]float64{}}
	for _, l := range r.Legs {
		for _, p := range l.Shape {
			out.Coordinates = append(out.Coordinates, []float64{p.Lon, p.Lat})
		}
	}
	return out
}

func (s *Server) GetRoutingStatus(ctx context.Context) (*gen.RoutingStatus, error) {
	st, err := s.routing.Status(ctx)
	if err != nil {
		if errors.Is(err, routing.ErrUnavailable) {
			return &gen.RoutingStatus{Available: false}, nil
		}
		return nil, err
	}
	out := &gen.RoutingStatus{Available: true, Version: gen.NewOptString(st.Version)}
	if st.Tileset > 0 {
		out.GraphBuiltAt = gen.NewOptDateTime(time.Unix(st.Tileset, 0))
	}
	return out, nil
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-faster/jx"

	"freightflow/internal/api/gen"
	"freightflow/internal/platform/problem"
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

func vehicleProfile(v gen.VehicleParams) routing.Profile {
	return routing.Profile{
		GrossT: v.GrossT, PayloadT: v.PayloadT.Or(0), AxleLoadT: v.AxleLoadT.Or(0), HeightM: v.HeightM,
		WidthM: v.WidthM, LengthM: v.LengthM, EcoClass: v.EcoClass.Or(0), Hazmat: v.Hazmat.Or(false),
	}
}

func vehiclePasses(in []gen.VehiclePass) []routing.Pass {
	out := make([]routing.Pass, len(in))
	for i, p := range in {
		out[i] = routing.Pass{Zone: string(p.Zone), Kind: string(p.Kind)}
	}
	return out
}

func departTime(t gen.OptDateTime) time.Time {
	if v, ok := t.Get(); ok {
		return v
	}
	return time.Now()
}

func zoneVerdicts(ds []routing.ZoneDecision) []gen.ZoneVerdict {
	out := make([]gen.ZoneVerdict, len(ds))
	for i, d := range ds {
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
		out[i] = z
	}
	return out
}

// spend расходует лимит расчётов маршрутизации пользователя; cost — стоимость в расчётах.
func (s *Server) spend(ctx context.Context, cost int) error {
	if s.routeLimit.AllowN(principal(ctx).UserID.String(), cost) {
		return nil
	}
	return problem.TooMany.WithDetail(fmt.Sprintf(
		"Не более %d расчётов маршрутизации подряд, затем один в секунду — повторите через несколько секунд", routingBurst))
}

func (s *Server) BuildRoute(ctx context.Context, req *gen.RouteRequest) (*gen.RouteResult, error) {
	if err := s.spend(ctx, 1); err != nil {
		return nil, err
	}
	in := routing.RouteInput{
		Profile: vehicleProfile(req.Vehicle), Passes: vehiclePasses(req.Passes), Depart: departTime(req.Depart),
		Alternates: req.Alternates.Or(0),
	}
	for _, p := range req.Points {
		in.Points = append(in.Points, routing.Point{Lon: p.Lon, Lat: p.Lat})
	}
	res, err := s.routing.Route(ctx, in)
	if err != nil && !errors.Is(err, routing.ErrBlocked) {
		return nil, err
	}
	out := &gen.RouteResult{
		Routes:       make([]gen.RouteLine, len(res.Routes)),
		Zones:        zoneVerdicts(res.Zones),
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

func (s *Server) BuildIsochrone(ctx context.Context, req *gen.IsochroneRequest) (*gen.IsochroneResult, error) {
	if err := s.spend(ctx, 2); err != nil {
		return nil, err
	}
	res, err := s.routing.Isochrone(ctx, routing.IsochroneInput{
		Origin: routing.Point{Lon: req.Origin.Lon, Lat: req.Origin.Lat}, Profile: vehicleProfile(req.Vehicle),
		Passes: vehiclePasses(req.Passes), Depart: departTime(req.Depart), Minutes: req.Minutes,
	})
	if err != nil && !errors.Is(err, routing.ErrBlocked) {
		return nil, err
	}
	out := &gen.IsochroneResult{Contours: []gen.IsochroneContour{}, Zones: zoneVerdicts(res.Zones)}
	if res.Blocked != nil {
		out.Blocked = gen.NewOptBlockedPoint(gen.BlockedPoint{Index: 0, Zone: gen.BlockedPointZone(res.Blocked.Zone)})
	}
	if len(res.GeoJSON) == 0 {
		return out, nil
	}
	var fc struct {
		Features []struct {
			Properties struct {
				Contour float64 `json:"contour"`
			} `json:"properties"`
			Geometry map[string]json.RawMessage `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(res.GeoJSON, &fc); err != nil {
		return nil, fmt.Errorf("isochrone: %w", err)
	}
	for _, f := range fc.Features {
		geom := gen.IsochroneContourGeometry{}
		for k, v := range f.Geometry {
			geom[k] = jx.Raw(v)
		}
		out.Contours = append(out.Contours, gen.IsochroneContour{Minutes: int(f.Properties.Contour), Geometry: geom})
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

func (s *Server) GetNetworkAnalysis(ctx context.Context, params gen.GetNetworkAnalysisParams) (*gen.NetworkAnalysis, error) {
	sum, err := s.routing.NetworkAnalysis(ctx, params.Top.Or(20))
	if err != nil {
		return nil, err
	}
	accuracy := func(a routing.Accuracy) gen.CentralityAccuracy {
		out := gen.CentralityAccuracy{Sources: a.Sources, Exact: a.Exact}
		if a.RSE != nil {
			out.Rse = gen.NewOptFloat64(*a.RSE)
		}
		if a.SplitHalf != nil {
			out.SplitHalf = gen.NewOptFloat64(*a.SplitHalf)
		}
		return out
	}
	out := &gen.NetworkAnalysis{
		Version: sum.Version, Profile: sum.Profile, BuiltAt: sum.BuiltAt, Nodes: sum.Nodes, Edges: sum.Edges,
		Arcs: sum.Arcs, LargestComponent: sum.LargestComponent, Accuracy: accuracy(sum.Profiled),
		FreeAccuracy: accuracy(sum.Free), DurationSeconds: sum.Duration.Seconds(),
		TopStreets: make([]gen.StreetRank, len(sum.TopStreets)), Bottlenecks: make([]gen.NetworkBottleneck, len(sum.Bottlenecks)),
		BottleneckCount: sum.BottleneckCount, NoDetourCount: sum.NoDetourCount,
		Criticality: gen.CriticalityStats{
			Facilities: sum.Criticality.Facilities, FacilitiesSnapped: sum.Criticality.FacilitiesSnapped,
			Targets: sum.Criticality.Targets, TargetsSnapped: sum.Criticality.TargetsSnapped,
			Elements: sum.Criticality.Elements, Bridges: sum.Criticality.Bridges, Confirmed: sum.Criticality.Confirmed,
		},
		Critical: make([]gen.CriticalElement, len(sum.Critical)),
	}
	damage := func(d routing.Damage) gen.ElementDamage {
		return gen.ElementDamage{Facilities: d.Facilities, Minutes: d.Minutes, MaxDelay: d.MaxDelay, Disconnected: d.Disconnected}
	}
	for i, el := range sum.Critical {
		v := gen.CriticalElement{
			ID: el.ID, Kind: gen.CriticalElementKind(el.Kind), Name: el.Name, Rank: el.Rank, Graph: damage(el.Graph),
			Point: gen.LngLat{Lon: el.Point.Lon, Lat: el.Point.Lat}, Same: el.Same,
		}
		if el.Valhalla != nil {
			v.Valhalla = gen.NewOptElementDamage(damage(*el.Valhalla))
		}
		out.Critical[i] = v
	}
	for i, r := range sum.TopStreets {
		out.TopStreets[i] = gen.StreetRank{
			Name: r.Name, Share: r.Share, Rank: r.BestRank, TopKm: r.TopKm,
			Point: gen.LngLat{Lon: r.Point.Lon, Lat: r.Point.Lat},
		}
	}
	for i, b := range sum.Bottlenecks {
		v := gen.NetworkBottleneck{
			SegmentId: b.Segment, Name: b.Name, Kind: gen.NetworkBottleneckKind(b.Kind), FreeRank: b.FreeRank,
			FreeShare: b.FreeShare, Point: gen.LngLat{Lon: b.Point.Lon, Lat: b.Point.Lat}, Segments: b.Segments,
		}
		if b.Value != nil {
			v.Value = gen.NewOptFloat64(*b.Value)
		}
		if b.DetourMin != nil {
			v.DetourMinutes = gen.NewOptFloat64(*b.DetourMin)
		}
		out.Bottlenecks[i] = v
	}
	return out, nil
}

func (s *Server) SearchPlaces(ctx context.Context, params gen.SearchPlacesParams) ([]gen.Place, error) {
	list, err := s.routing.Places(ctx, params.Q, params.Limit.Or(8))
	if err != nil {
		return nil, err
	}
	out := make([]gen.Place, len(list))
	for i, p := range list {
		out[i] = gen.Place{
			Kind: gen.PlaceKind(p.Kind), ID: p.ID, Title: p.Title, Subtitle: p.Subtitle,
			Point: gen.LngLat{Lon: p.Point.Lon, Lat: p.Point.Lat},
		}
	}
	return out, nil
}

func (s *Server) BuildMatrix(ctx context.Context, req *gen.MatrixRequest) (*gen.MatrixResult, error) {
	// Матрица — расчёт на каждые 100 пар.
	if err := s.spend(ctx, 1+len(req.Sources)*len(req.Targets)/100); err != nil {
		return nil, err
	}
	points := func(in []gen.LngLat) []routing.Point {
		out := make([]routing.Point, len(in))
		for i, p := range in {
			out[i] = routing.Point{Lon: p.Lon, Lat: p.Lat}
		}
		return out
	}
	res, err := s.routing.Matrix(ctx, routing.MatrixInput{
		Sources: points(req.Sources), Targets: points(req.Targets), Profile: vehicleProfile(req.Vehicle),
		Passes: vehiclePasses(req.Passes), Depart: departTime(req.Depart),
	})
	if err != nil {
		return nil, err
	}
	blocked := func(in []routing.BlockedPoint) []gen.BlockedPoint {
		out := make([]gen.BlockedPoint, len(in))
		for i, b := range in {
			out[i] = gen.BlockedPoint{Index: b.Index, Zone: gen.BlockedPointZone(b.Zone)}
		}
		return out
	}
	out := &gen.MatrixResult{
		Rows: make([][]gen.MatrixCell, len(res.Cells)), Zones: zoneVerdicts(res.Zones),
		BlockedSources: blocked(res.BlockedSources), BlockedTargets: blocked(res.BlockedTargets),
	}
	for i, row := range res.Cells {
		out.Rows[i] = make([]gen.MatrixCell, len(row))
		for j, c := range row {
			if c.Reachable {
				out.Rows[i][j] = gen.MatrixCell{TimeS: gen.NewOptFloat64(c.Time), LengthKm: gen.NewOptFloat64(c.Length)}
			}
		}
	}
	return out, nil
}

package api

import (
	"context"
	"encoding/json"
	"fmt"

	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/routing"
	"freightflow/internal/scenario"
)

// recentScenarios — сценариев в списке.
const recentScenarios = 100

// recode переносит значение между типами с одинаковым JSON (параметры и итог сценария
// совпадают в модуле и в API).
func recode(dst, src any) error {
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("recode %T: %w", dst, err)
	}
	return nil
}

func toScenario(sc *scenario.Scenario) (gen.Scenario, error) {
	out := gen.Scenario{
		ID: sc.ID, Name: sc.Name, Description: sc.Description, Status: gen.ScenarioStatus(sc.Status),
		Progress:  gen.ScenarioProgress{Done: sc.Progress.Done, Total: sc.Progress.Total},
		CreatedAt: sc.CreatedAt,
	}
	if sc.Progress.Stage != "" {
		out.Progress.Stage = gen.NewOptString(sc.Progress.Stage)
	}
	if err := recode(&out.Params, sc.Params); err != nil {
		return out, err
	}
	if sc.Summary != nil {
		var sum gen.ScenarioSummary
		if err := recode(&sum, sc.Summary); err != nil {
			return out, err
		}
		out.Summary = gen.NewOptScenarioSummary(sum)
	}
	if sc.Error != nil {
		out.Error = gen.NewOptString(*sc.Error)
	}
	if sc.GraphBuiltAt != nil {
		out.GraphBuiltAt = gen.NewOptDateTime(*sc.GraphBuiltAt)
	}
	if sc.StartedAt != nil {
		out.StartedAt = gen.NewOptDateTime(*sc.StartedAt)
	}
	if sc.FinishedAt != nil {
		out.FinishedAt = gen.NewOptDateTime(*sc.FinishedAt)
	}
	return out, nil
}

func (s *Server) CreateScenario(ctx context.Context, req *gen.ScenarioRequest) (*gen.Scenario, error) {
	p := principal(ctx)
	var params scenario.Params
	if err := recode(&params, &req.Params); err != nil {
		return nil, err
	}
	sc, err := s.scenarios.Create(ctx, p.UserID, req.Name, req.Description.Or(""), params)
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "scenario.created", EntityType: "scenario", EntityID: sc.ID.String(),
		Details: map[string]any{"name": sc.Name, "closures": len(sc.Params.Closures), "targets": sc.Params.Targets},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out, err := toScenario(sc)
	return &out, err
}

func (s *Server) ListScenarios(ctx context.Context) ([]gen.Scenario, error) {
	list, err := s.scenarios.List(ctx, principal(ctx).UserID, recentScenarios)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Scenario, len(list))
	for i := range list {
		if out[i], err = toScenario(&list[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Server) GetScenario(ctx context.Context, params gen.GetScenarioParams) (*gen.Scenario, error) {
	sc, err := s.scenarios.Get(ctx, principal(ctx).UserID, params.ScenarioId)
	if err != nil {
		return nil, err
	}
	out, err := toScenario(sc)
	return &out, err
}

func (s *Server) DeleteScenario(ctx context.Context, params gen.DeleteScenarioParams) error {
	p := principal(ctx)
	if err := s.scenarios.Delete(ctx, p.UserID, params.ScenarioId); err != nil {
		return err
	}
	return s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "scenario.deleted", EntityType: "scenario", EntityID: params.ScenarioId.String(),
	}, meta(ctx))
}

func (s *Server) RerunScenario(ctx context.Context, params gen.RerunScenarioParams) (*gen.Scenario, error) {
	sc, err := s.scenarios.Rerun(ctx, principal(ctx).UserID, params.ScenarioId)
	if err != nil {
		return nil, err
	}
	out, err := toScenario(sc)
	return &out, err
}

func optMinutes(v *float64) gen.OptFloat64 {
	if v == nil {
		return gen.OptFloat64{}
	}
	return gen.NewOptFloat64(*v)
}

func (s *Server) ScenarioFacilities(ctx context.Context, params gen.ScenarioFacilitiesParams) ([]gen.ScenarioFacility, error) {
	rows, err := s.scenarios.Facilities(ctx, principal(ctx).UserID, params.ScenarioId, string(params.Target),
		params.All.Or(false), params.Limit.Or(500))
	if err != nil {
		return nil, err
	}
	out := make([]gen.ScenarioFacility, len(rows))
	for i, r := range rows {
		out[i] = gen.ScenarioFacility{
			Entity: r.Entity, Name: r.Name, District: r.District, Point: gen.LngLat{Lon: r.Lon, Lat: r.Lat},
			BaseMin: optMinutes(r.BaseMin), ScenarioMin: optMinutes(r.ScenarioMin),
			BaseStatus: gen.ScenarioReach(r.BaseStatus), ScenarioStatus: gen.ScenarioReach(r.ScenarioStatus),
		}
	}
	return out, nil
}

func (s *Server) ScenarioDistricts(ctx context.Context, params gen.ScenarioDistrictsParams) ([]gen.ScenarioDistrict, error) {
	rows, err := s.scenarios.Districts(ctx, principal(ctx).UserID, params.ScenarioId, string(params.Target))
	if err != nil {
		return nil, err
	}
	out := make([]gen.ScenarioDistrict, len(rows))
	for i, r := range rows {
		out[i] = gen.ScenarioDistrict{
			District: r.District, Facilities: r.Facilities, Affected: r.Affected, Lost: r.Lost,
			DelayMin: r.DelayMin, MaxDelay: r.MaxDelay,
		}
	}
	return out, nil
}

func (s *Server) ScenarioIsochrones(ctx context.Context, params gen.ScenarioIsochronesParams) (gen.ScenarioIsochronesOK, error) {
	raw, err := s.scenarios.Isochrones(ctx, principal(ctx).UserID, params.ScenarioId)
	if err != nil {
		return nil, err
	}
	obj, err := rawObject(raw)
	return gen.ScenarioIsochronesOK(obj), err
}

func (s *Server) ListScenarioElements(ctx context.Context, params gen.ListScenarioElementsParams) ([]gen.ScenarioElement, error) {
	els, err := s.routing.SearchElements(ctx, params.Q.Or(""), params.Limit.Or(50))
	if err != nil {
		return nil, err
	}
	out := make([]gen.ScenarioElement, len(els))
	for i, e := range els {
		geom, err := rawObject(e.Geometry)
		if err != nil {
			return nil, err
		}
		out[i] = gen.ScenarioElement{
			ID: e.ID, Kind: gen.ScenarioElementKind(e.Kind), Name: e.Name, Rank: e.Rank, DamageMin: e.DamageMin,
			Geometry: gen.ScenarioElementGeometry(geom),
		}
	}
	return out, nil
}

func (s *Server) ScenarioSegmentAt(ctx context.Context, params gen.ScenarioSegmentAtParams) (*gen.ScenarioSegment, error) {
	seg, err := s.routing.SegmentAt(ctx, routing.Point{Lon: params.Lon, Lat: params.Lat})
	if err != nil {
		return nil, err
	}
	geom, err := rawObject(seg.Geometry)
	if err != nil {
		return nil, err
	}
	return &gen.ScenarioSegment{
		ID: seg.ID, Name: seg.Name, Class: seg.Class, LengthM: seg.LengthM, Geometry: gen.ScenarioSegmentGeometry(geom),
	}, nil
}

func (s *Server) ScenarioClosures(ctx context.Context, params gen.ScenarioClosuresParams) (gen.ScenarioClosuresOK, error) {
	raw, err := s.scenarios.Closures(ctx, principal(ctx).UserID, params.ScenarioId)
	if err != nil {
		return nil, err
	}
	obj, err := rawObject(raw)
	return gen.ScenarioClosuresOK(obj), err
}

func toResilienceRun(r *scenario.ResilienceRun) gen.ResilienceRun {
	out := gen.ResilienceRun{
		ID: r.ID, GraphBuiltAt: r.GraphBuiltAt, Profile: r.Profile, RadiusKm: r.RadiusKm,
		Status: gen.ResilienceRunStatus(r.Status), Elements: r.Elements, StartedAt: r.StartedAt,
	}
	for _, t := range r.Targets {
		out.Targets = append(out.Targets, gen.ScenarioTargetKind(t))
	}
	if r.Error != nil {
		out.Error = gen.NewOptString(*r.Error)
	}
	if r.FinishedAt != nil {
		out.FinishedAt = gen.NewOptDateTime(*r.FinishedAt)
	}
	return out
}

func (s *Server) GetResilience(ctx context.Context) (*gen.Resilience, error) {
	last, running, rows, err := s.scenarios.Resilience(ctx)
	if err != nil {
		return nil, err
	}
	out := &gen.Resilience{Elements: make([]gen.ResilienceElement, len(rows))}
	if last != nil {
		out.Run = gen.NewOptResilienceRun(toResilienceRun(last))
	}
	if running != nil {
		out.Running = gen.NewOptResilienceRun(toResilienceRun(running))
	}
	for i, r := range rows {
		by := make([]gen.ResilienceTarget, len(r.ByTarget))
		for j, t := range r.ByTarget {
			by[j] = gen.ResilienceTarget{
				Kind: gen.ScenarioTargetKind(t.Kind), Affected: t.Affected, Lost: t.Lost, DelayMin: t.DelayMin,
				MaxDelay: t.MaxDelay, Damage: t.Damage,
			}
		}
		out.Elements[i] = gen.ResilienceElement{
			ElementId: r.ElementID, Kind: gen.ResilienceElementKind(r.Kind), Name: r.Name, Rank: r.Rank,
			CriticalityRank: r.CriticalityRank, Candidates: r.Candidates, Affected: r.Affected, Lost: r.Lost,
			DelayMin: r.DelayMin, MaxDelayMin: r.MaxDelayMin, Damage: r.Damage, ByTarget: by,
			Point: gen.LngLat{Lon: r.Lon, Lat: r.Lat},
		}
	}
	return out, nil
}

func (s *Server) RunResilience(ctx context.Context) error {
	p := principal(ctx)
	if err := s.scenarios.EnqueueResilience(ctx); err != nil {
		return err
	}
	return s.identity.Audit(ctx, &p, identity.AuditEntry{Action: "resilience.queued", EntityType: "resilience"}, meta(ctx))
}

package api

import (
	"context"

	"freightflow/internal/analytics"
	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/platform/problem"
)

// --- Сводные индексы ---

// componentInfo — название и единица составляющей (показатель или индекс).
func componentInfo(ref string) (string, string) {
	if ix, ok := analytics.FindIndex(ref); ok {
		return ix.Title, "0–100"
	}
	if m, ok := analytics.Find(ref); ok {
		return m.Title, m.Unit
	}
	return ref, ""
}

func (s *Server) ListIndexes(ctx context.Context) ([]gen.IndexDef, error) {
	pr := principal(ctx)
	out := make([]gen.IndexDef, 0, len(analytics.Indexes))
	for _, ix := range analytics.Indexes {
		cfg, own, err := s.analytics.ConfigFor(ctx, ix.Code, pr.UserID)
		if err != nil {
			return nil, err
		}
		def := gen.IndexDef{
			Code: ix.Code, Title: ix.Title, Method: ix.Method, Own: own,
			Normalization: gen.IndexDefNormalization(cfg.Normalization),
		}
		for _, level := range []string{analytics.LevelDistrict, analytics.LevelH3r8} {
			comps, ok := ix.Levels[level]
			if !ok {
				continue
			}
			l := gen.IndexDefLevelsItem{Level: gen.IndexDefLevelsItemLevel(level)}
			for _, cm := range comps {
				title, unit := componentInfo(cm.Ref)
				l.Components = append(l.Components, gen.IndexDefLevelsItemComponentsItem{
					Ref: cm.Ref, Title: title, Unit: unit, Direction: gen.IndexDefLevelsItemComponentsItemDirection(cm.Direction),
					Weight: cfg.Weights[level][cm.Ref], DefaultWeight: cm.Weight,
				})
			}
			def.Levels = append(def.Levels, l)
		}
		out = append(out, def)
	}
	return out, nil
}

func (s *Server) ComputeIndex(ctx context.Context, req *gen.ComputeIndexReq, p gen.ComputeIndexParams) (*gen.IndexResult, error) {
	cfg, _, err := s.analytics.ConfigFor(ctx, p.Code, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	level := string(req.Level)
	if n, ok := req.Normalization.Get(); ok {
		cfg.Normalization = string(n)
	}
	if req.Weights.Set {
		cfg.Weights[level] = map[string]float64(req.Weights.Value)
	}
	res, err := s.analytics.Compute(ctx, p.Code, level, cfg)
	if err != nil {
		return nil, err
	}
	out := &gen.IndexResult{
		Code: res.Code, Level: res.Level, Normalization: gen.IndexResultNormalization(res.Normalization),
		Weights: gen.IndexResultWeights(res.Weights), Scores: make([]gen.IndexResultScoresItem, len(res.Scores)),
		Sensitivity: make([]gen.IndexResultSensitivityItem, len(res.Sensitivity)),
	}
	for i, sc := range res.Scores {
		item := gen.IndexResultScoresItem{Entity: sc.Entity, Label: sc.Label, Score: sc.Score, Rank: sc.Rank}
		for _, pt := range sc.Parts {
			item.Parts = append(item.Parts, gen.IndexResultScoresItemPartsItem{
				Ref: pt.Ref, Value: pt.Value, Normalized: pt.Normalized, Weight: pt.Weight,
			})
		}
		out.Scores[i] = item
	}
	for i, se := range res.Sensitivity {
		out.Sensitivity[i] = gen.IndexResultSensitivityItem{
			Ref: se.Ref, RhoMinus: se.RhoMinus, RhoPlus: se.RhoPlus, MaxShift: se.MaxShift, Entities: se.Entities,
		}
	}
	return out, nil
}

func (s *Server) SaveIndexConfig(ctx context.Context, req *gen.SaveIndexConfigReq, p gen.SaveIndexConfigParams) error {
	pr := principal(ctx)
	cfg := analytics.Config{Normalization: string(req.Normalization), Weights: map[string]map[string]float64{}}
	for level, ws := range req.Weights {
		cfg.Weights[level] = map[string]float64(ws)
	}
	owner := &pr.UserID
	if req.Scope == gen.SaveIndexConfigReqScopeDefault {
		// Веса по умолчанию для всех — у администратора настроек.
		if !pr.Has(identity.PermSettingsManage) {
			return problem.Forbidden
		}
		owner = nil
	}
	if err := s.analytics.SaveConfig(ctx, p.Code, owner, pr.UserID, cfg); err != nil {
		return err
	}
	return s.identity.Audit(ctx, &pr, identity.AuditEntry{
		Action: "index.config_saved", EntityType: "index", EntityID: p.Code,
		Details: map[string]any{"scope": string(req.Scope), "normalization": cfg.Normalization},
	}, meta(ctx))
}

func (s *Server) ResetIndexConfig(ctx context.Context, p gen.ResetIndexConfigParams) error {
	return s.analytics.ResetConfig(ctx, p.Code, principal(ctx).UserID)
}

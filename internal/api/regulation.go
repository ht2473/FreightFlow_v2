package api

import (
	"context"
	"time"

	"freightflow/internal/api/gen"
	"freightflow/internal/regulation"
)

// --- Регулирование ---

func (s *Server) GetRegulation(ctx context.Context) (*gen.RegulationOverview, error) {
	o, err := s.regulation.Overview(ctx)
	if err != nil {
		return nil, err
	}
	out := &gen.RegulationOverview{
		Zones:     make([]gen.RingZone, len(o.Zones)),
		Rules:     make([]gen.ZoneRule, len(o.Rules)),
		Documents: make([]gen.LegalDocument, len(o.Documents)),
		Pages:     make([]gen.OfficialPage, len(o.Pages)),
		CargoFrame: gen.CargoFrame{
			Streets: o.Frame.Streets, Matched: o.Frame.Matched, Partial: o.Frame.Partial, LengthKm: o.Frame.LengthKm,
			UpdatedAt: optDateTime(o.Frame.UpdatedAt), Okrugs: make([]gen.CargoFrameOkrug, len(o.Frame.Okrugs)),
		},
	}
	for i, z := range o.Zones {
		out.Zones[i] = gen.RingZone{Code: gen.RingZoneCode(z.Code), Name: z.Name, LengthKm: z.LengthKm}
		if z.AreaKm2 != nil {
			out.Zones[i].AreaKm2 = gen.NewOptFloat64(*z.AreaKm2)
		}
	}
	for i, r := range o.Rules {
		out.Rules[i] = toZoneRule(r)
	}
	for i, d := range o.Documents {
		out.Documents[i] = gen.LegalDocument{
			Code: d.Code, Kind: gen.LegalDocumentKind(d.Kind), Title: d.Title, Authority: d.Authority,
			Status: gen.LegalDocumentStatus(d.Status), Number: optString(d.Number), URL: optString(d.URL),
			Amends: optString(d.Amends), Summary: optString(d.Summary), SignedOn: optDate(d.SignedOn),
			EffectiveOn: optDate(d.EffectiveOn), VerifiedOn: optDate(d.VerifiedOn),
		}
	}
	for i, p := range o.Pages {
		out.Pages[i] = gen.OfficialPage{
			Code: p.Code, Title: p.Title, URL: p.URL, FetchedAt: p.FetchedAt, ChangedAt: p.ChangedAt,
			ChangedSinceVerification: p.ChangedSinceVerification,
		}
	}
	for i, k := range o.Frame.Okrugs {
		out.CargoFrame.Okrugs[i] = gen.CargoFrameOkrug{Okrug: k.Okrug, Streets: k.Streets, Matched: k.Matched, LengthKm: k.LengthKm}
	}
	return out, nil
}

func toZoneRule(r regulation.Rule) gen.ZoneRule {
	out := gen.ZoneRule{
		Code: r.Code, Zone: gen.ZoneRuleZone(r.Zone), Title: r.Title, Effect: gen.ZoneRuleEffect(r.Effect),
		TimeFrom: optString(r.TimeFrom), TimeTo: optString(r.TimeTo), PassZones: r.PassZones,
		ValidFrom: optDate(r.ValidFrom), ValidTo: optDate(r.ValidTo), Document: r.Document, Clause: optString(r.Clause),
		Confirmed: r.Confirmed, ConfirmedBy: optString(r.ConfirmedBy), Note: optString(r.Note),
	}
	if r.MassBasis != nil {
		out.MassBasis = gen.NewOptZoneRuleMassBasis(gen.ZoneRuleMassBasis(*r.MassBasis))
	}
	if r.MassOverT != nil {
		out.MassOverT = gen.NewOptFloat64(*r.MassOverT)
	}
	if r.EcoClassMin != nil {
		out.EcoClassMin = gen.NewOptInt(*r.EcoClassMin)
	}
	if r.PassKind != nil {
		out.PassKind = gen.NewOptZoneRulePassKind(gen.ZoneRulePassKind(*r.PassKind))
	}
	return out
}

func (s *Server) GetPassStats(ctx context.Context) (*gen.PassStats, error) {
	series, err := s.regulation.PassStats(ctx)
	if err != nil {
		return nil, err
	}
	out := &gen.PassStats{Series: make([]gen.PassSeries, len(series))}
	for i, ps := range series {
		out.Series[i] = gen.PassSeries{
			Zone: ps.Zone, Series: ps.Series, Metric: gen.PassSeriesMetric(ps.Metric), Points: toMonthValues(ps.Points),
		}
	}
	out.UpdatedAt, err = s.sourceUpdatedAt(ctx, "mos_passes")
	return out, err
}

func (s *Server) GetPermitStats(ctx context.Context) (*gen.PermitStats, error) {
	st, err := s.regulation.PermitStats(ctx)
	if err != nil {
		return nil, err
	}
	out := &gen.PermitStats{Total: st.Total, Months: toMonthValues(st.Months), Exceeded: make([]gen.NamedCount, len(st.Exceeded))}
	for i, t := range st.Exceeded {
		out.Exceeded[i] = gen.NamedCount{Name: t.Name, Count: t.Count}
	}
	out.UpdatedAt, err = s.sourceUpdatedAt(ctx, "mos_special_permits")
	return out, err
}

// sourceUpdatedAt — время последней успешной загрузки источника, если он подключён.
func (s *Server) sourceUpdatedAt(ctx context.Context, code string) (gen.OptDateTime, error) {
	list, err := s.ingest.ListSources(ctx)
	if err != nil {
		return gen.OptDateTime{}, err
	}
	for _, src := range list {
		if src.Code == code {
			return optDateTime(src.LastSuccessAt), nil
		}
	}
	return gen.OptDateTime{}, nil
}

func toMonthValues(in []regulation.MonthValue) []gen.MonthValue {
	out := make([]gen.MonthValue, len(in))
	for i, m := range in {
		out[i] = gen.MonthValue{Month: m.Month, Value: m.Value}
	}
	return out
}

func optString(v *string) gen.OptString {
	if v == nil {
		return gen.OptString{}
	}
	return gen.NewOptString(*v)
}

func optDate(v *time.Time) gen.OptDate {
	if v == nil {
		return gen.OptDate{}
	}
	return gen.NewOptDate(*v)
}

func optDateTime(v *time.Time) gen.OptDateTime {
	if v == nil {
		return gen.OptDateTime{}
	}
	return gen.NewOptDateTime(*v)
}

func (s *Server) GetSignQuality(ctx context.Context) (*gen.SignQuality, error) {
	q, err := s.regulation.SignQuality(ctx)
	if err != nil {
		return nil, err
	}
	out := &gen.SignQuality{
		Active: q.Active, RestrictionSigns: q.RestrictionSigns, Matched: q.Matched, Ambiguous: q.Ambiguous,
		Unmatched: q.Unmatched, Restrictions: q.Restrictions,
		Types: make([]gen.SignTypeQuality, len(q.Types)), Conflicts: make([]gen.ConflictCount, len(q.Conflicts)),
	}
	for i, t := range q.Types {
		out.Types[i] = gen.SignTypeQuality{
			Code: t.Code, Name: t.Name, Kind: t.Kind, Count: t.Count, Matched: t.Matched, Ambiguous: t.Ambiguous, Unmatched: t.Unmatched,
		}
	}
	for i, c := range q.Conflicts {
		out.Conflicts[i] = gen.ConflictCount{Type: gen.ConflictCountType(c.Type), Kind: c.Kind, Segments: c.Segments}
	}
	out.UpdatedAt, err = s.sourceUpdatedAt(ctx, "mos_road_signs")
	return out, err
}

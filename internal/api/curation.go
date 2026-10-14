package api

import (
	"context"

	"freightflow/internal/api/gen"
	"freightflow/internal/facility"
	"freightflow/internal/identity"
)

// --- Уточнение реестра ---

func toCuration(c facility.Curation) gen.Curation {
	out := gen.Curation{
		ID: c.ID, Field: gen.CurationField(c.Field), Value: optStr(c.Value), Reason: c.Reason, Author: optStr(c.Author),
		CreatedAt: c.CreatedAt, RevokedBy: optStr(c.RevokedBy),
	}
	if c.RevokedAt != nil {
		out.RevokedAt = gen.NewOptDateTime(*c.RevokedAt)
	}
	return out
}

func toFacilityReport(r facility.Report) gen.FacilityReport {
	out := gen.FacilityReport{
		ID: r.ID, FacilityId: r.FacilityID, FacilityName: r.FacilityName, Message: r.Message, Author: optStr(r.Author),
		Status: gen.FacilityReportStatus(r.Status), Resolution: optStr(r.Resolution), ResolvedBy: optStr(r.ResolvedBy),
		CreatedAt: r.CreatedAt,
	}
	if r.ResolvedAt != nil {
		out.ResolvedAt = gen.NewOptDateTime(*r.ResolvedAt)
	}
	return out
}

func (s *Server) FacilityHistory(ctx context.Context, p gen.FacilityHistoryParams) (*gen.FacilityHistory, error) {
	if _, err := s.facilities.Get(ctx, p.FacilityId); err != nil {
		return nil, err
	}
	h, err := s.facilities.History(ctx, p.FacilityId)
	if err != nil {
		return nil, err
	}
	out := &gen.FacilityHistory{Curations: make([]gen.Curation, len(h.Curations)), Reports: make([]gen.FacilityReport, len(h.Reports))}
	for i, c := range h.Curations {
		out.Curations[i] = toCuration(c)
	}
	for i, r := range h.Reports {
		out.Reports[i] = toFacilityReport(r)
	}
	return out, nil
}

func (s *Server) CurateFacility(ctx context.Context, req *gen.CurationRequest, p gen.CurateFacilityParams) (*gen.Curation, error) {
	pr := principal(ctx)
	value := req.Value
	c, err := s.facilities.Curate(ctx, p.FacilityId, pr.UserID, string(req.Field), &value, req.Reason)
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &pr, identity.AuditEntry{
		Action: "facility.curated", EntityType: "facility", EntityID: p.FacilityId.String(),
		Details: map[string]any{"field": c.Field, "value": value, "reason": c.Reason},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out := toCuration(*c)
	return &out, nil
}

func (s *Server) RevokeCuration(ctx context.Context, p gen.RevokeCurationParams) error {
	pr := principal(ctx)
	if err := s.facilities.RevokeCuration(ctx, p.FacilityId, p.CurationId, pr.UserID); err != nil {
		return err
	}
	return s.identity.Audit(ctx, &pr, identity.AuditEntry{
		Action: "facility.curation_revoked", EntityType: "facility", EntityID: p.FacilityId.String(),
		Details: map[string]any{"curation": p.CurationId.String()},
	}, meta(ctx))
}

func (s *Server) ReportFacilityIssue(ctx context.Context, req *gen.ReportFacilityIssueReq, p gen.ReportFacilityIssueParams) (*gen.FacilityReport, error) {
	r, err := s.facilities.ReportIssue(ctx, p.FacilityId, principal(ctx).UserID, req.Message)
	if err != nil {
		return nil, err
	}
	out := toFacilityReport(*r)
	return &out, nil
}

// maxReports — сообщений в очереди за запрос.
const maxReports = 200

func (s *Server) ListFacilityReports(ctx context.Context, p gen.ListFacilityReportsParams) ([]gen.FacilityReport, error) {
	list, err := s.facilities.Reports(ctx, string(p.Status.Or(gen.ListFacilityReportsStatusOpen)), maxReports)
	if err != nil {
		return nil, err
	}
	out := make([]gen.FacilityReport, len(list))
	for i, r := range list {
		out[i] = toFacilityReport(r)
	}
	return out, nil
}

func (s *Server) ResolveFacilityReport(ctx context.Context, req *gen.ResolveFacilityReportReq, p gen.ResolveFacilityReportParams) (*gen.FacilityReport, error) {
	pr := principal(ctx)
	r, err := s.facilities.ResolveReport(ctx, p.ReportId, pr.UserID, string(req.Status), req.Resolution.Or(""))
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &pr, identity.AuditEntry{
		Action: "facility.report_resolved", EntityType: "facility", EntityID: r.FacilityID.String(),
		Details: map[string]any{"report": r.ID.String(), "status": r.Status},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out := toFacilityReport(*r)
	return &out, nil
}

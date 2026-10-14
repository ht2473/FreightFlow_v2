package api

import (
	"context"
	"fmt"

	"freightflow/internal/api/gen"
	"freightflow/internal/facility"
	"freightflow/internal/identity"
	"freightflow/internal/report"
)

// --- Выгрузки ---

// recentExports — выгрузок в списке пользователя.
const recentExports = 20

func toExport(e *report.Export) gen.Export {
	out := gen.Export{
		ID: e.ID, Kind: gen.ExportKind(e.Kind), Format: gen.ExportFormat(e.Format), Status: gen.ExportStatus(e.Status),
		CreatedAt: e.CreatedAt, ExpiresAt: e.ExpiresAt,
	}
	if e.Rows != nil {
		out.Rows = gen.NewOptInt(*e.Rows)
	}
	if e.FileSize != nil {
		out.FileSize = gen.NewOptInt64(*e.FileSize)
	}
	if e.Error != nil {
		out.Error = gen.NewOptString(*e.Error)
	}
	if e.FinishedAt != nil {
		out.FinishedAt = gen.NewOptDateTime(*e.FinishedAt)
	}
	return out
}

func (s *Server) CreateExport(ctx context.Context, req *gen.ExportRequest) (*gen.Export, error) {
	p := principal(ctx)
	var params report.FacilityParams
	if f, ok := req.Filter.Get(); ok {
		rings := make([]gen.FacilityRingItem, len(f.Ring))
		for i, r := range f.Ring {
			rings[i] = gen.FacilityRingItem(r)
		}
		status := gen.OptFacilityStatus{}
		if v, ok := f.Status.Get(); ok {
			status = gen.NewOptFacilityStatus(gen.FacilityStatus(v))
		}
		params.Filter = facilityFilter(f.Q, f.Type, f.Okrug, f.District, rings, f.MinArea, f.MaxArea, f.MinConfidence,
			status, f.Bbox, f.Near, f.MaxMkadMin, f.NearRestrictions, f.HasPermit)
	} else {
		params.Filter = facility.Filter{Status: "active"}
	}
	params.Sort = req.Sort.Or(facility.DefaultSort)
	e, err := s.reports.CreateFacilities(ctx, p.UserID, string(req.Format), params)
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "export.created", EntityType: "export", EntityID: e.ID.String(),
		Details: map[string]any{"kind": e.Kind, "format": e.Format},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out := toExport(e)
	return &out, nil
}

func (s *Server) ListExports(ctx context.Context) ([]gen.Export, error) {
	list, err := s.reports.List(ctx, principal(ctx).UserID, recentExports)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Export, len(list))
	for i := range list {
		out[i] = toExport(&list[i])
	}
	return out, nil
}

func (s *Server) GetExport(ctx context.Context, params gen.GetExportParams) (*gen.Export, error) {
	e, err := s.reports.Get(ctx, principal(ctx).UserID, params.ExportId)
	if err != nil {
		return nil, err
	}
	out := toExport(e)
	return &out, nil
}

func (s *Server) GetExportFile(ctx context.Context, params gen.GetExportFileParams) (*gen.GetExportFileOKHeaders, error) {
	f, name, _, _, err := s.reports.File(ctx, principal(ctx).UserID, params.ExportId)
	if err != nil {
		return nil, err
	}
	// Файл закрывается, когда ответ отправлен (или клиент ушёл).
	context.AfterFunc(ctx, func() { _ = f.Close() })
	return &gen.GetExportFileOKHeaders{
		ContentDisposition: gen.NewOptString(fmt.Sprintf("attachment; filename=%q", name)),
		Response:           gen.GetExportFileOK{Data: f},
	}, nil
}

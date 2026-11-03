package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/report"
)

// recentReports — отчётов в списке.
const recentReports = 100

func (s *Server) ListReportTemplates(ctx context.Context) ([]gen.ReportTemplate, error) {
	p := principal(ctx)
	out := []gen.ReportTemplate{}
	for _, t := range report.Templates {
		if !p.Has(t.Permission) {
			continue
		}
		g := gen.ReportTemplate{
			Code: t.Code, Title: t.Title, Description: t.Description, Permission: string(t.Permission),
			Params: []gen.ReportTemplateParam{},
		}
		for _, pr := range t.Params {
			gp := gen.ReportTemplateParam{Name: pr.Name, Title: pr.Title, Kind: gen.ReportTemplateParamKind(pr.Kind), Required: pr.Required}
			if pr.Kind == "int" {
				gp.Min, gp.Max = gen.NewOptInt(pr.Min), gen.NewOptInt(pr.Max)
			}
			g.Params = append(g.Params, gp)
		}
		out = append(out, g)
	}
	return out, nil
}

func toReport(r *report.Report) *gen.Report {
	out := &gen.Report{
		ID: r.ID, Template: r.Template, Format: gen.ReportFormat(r.Format), Params: gen.ReportParams(r.Params), Title: r.Title,
		Status: gen.ReportStatus(r.Status), CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
	if out.Params == nil {
		out.Params = gen.ReportParams{}
	}
	if r.FileSize != nil {
		out.FileSize = gen.NewOptInt64(*r.FileSize)
	}
	if r.Error != nil {
		out.Error = gen.NewOptString(*r.Error)
	}
	if r.ScheduleID != nil {
		out.ScheduleId = gen.NewOptUUID(*r.ScheduleID)
	}
	if r.FinishedAt != nil {
		out.FinishedAt = gen.NewOptDateTime(*r.FinishedAt)
	}
	return out
}

// checkTemplate — у субъекта есть право на данные шаблона.
func checkTemplate(p identity.Principal, code string) error {
	t, ok := report.TemplateByCode(code)
	if !ok {
		return &report.ErrInvalid{Reason: fmt.Sprintf("неизвестный шаблон отчёта %q", code)}
	}
	if !p.Has(t.Permission) {
		return errForbidden
	}
	return nil
}

func (s *Server) CreateReport(ctx context.Context, req *gen.ReportRequest) (*gen.Report, error) {
	p := principal(ctx)
	if err := checkTemplate(p, req.Template); err != nil {
		return nil, err
	}
	r, err := s.reports.CreateReport(ctx, p.UserID, req.Template, string(req.Format), req.Params.Or(nil))
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "report.created", EntityType: "report", EntityID: r.ID.String(),
		Details: map[string]any{"template": r.Template, "format": r.Format, "params": r.Params},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	return toReport(r), nil
}

func (s *Server) ListReports(ctx context.Context) ([]gen.Report, error) {
	list, err := s.reports.Reports(ctx, principal(ctx).UserID, recentReports)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Report, len(list))
	for i := range list {
		out[i] = *toReport(&list[i])
	}
	return out, nil
}

func (s *Server) GetReport(ctx context.Context, params gen.GetReportParams) (*gen.Report, error) {
	r, err := s.reports.Report(ctx, principal(ctx).UserID, params.ReportId)
	if err != nil {
		return nil, err
	}
	return toReport(r), nil
}

func (s *Server) DeleteReport(ctx context.Context, params gen.DeleteReportParams) error {
	return s.reports.DeleteReport(ctx, principal(ctx).UserID, params.ReportId)
}

func (s *Server) GetReportFile(ctx context.Context, params gen.GetReportFileParams) (*gen.GetReportFileOKHeaders, error) {
	f, name, _, _, err := s.reports.ReportFile(ctx, principal(ctx).UserID, params.ReportId)
	if err != nil {
		return nil, err
	}
	context.AfterFunc(ctx, func() { _ = f.Close() })
	return &gen.GetReportFileOKHeaders{
		ContentDisposition: gen.NewOptString(fmt.Sprintf("attachment; filename=%q", name)),
		Response:           gen.GetReportFileOK{Data: f},
	}, nil
}

func toSchedule(sc *report.Schedule) *gen.ReportSchedule {
	out := &gen.ReportSchedule{
		ID: sc.ID, Template: sc.Template, Format: gen.ReportFormat(sc.Format), Params: gen.ReportScheduleParams(sc.Params),
		Title: sc.Title, Frequency: gen.ReportScheduleFrequency(sc.Frequency), Hour: sc.Hour, Recipients: sc.Recipients,
		Enabled: sc.Enabled, NextRunAt: sc.NextRunAt, CreatedAt: sc.CreatedAt,
	}
	if out.Params == nil {
		out.Params = gen.ReportScheduleParams{}
	}
	if out.Recipients == nil {
		out.Recipients = []string{}
	}
	if sc.Weekday != nil {
		out.Weekday = gen.NewOptInt(*sc.Weekday)
	}
	if sc.MonthDay != nil {
		out.MonthDay = gen.NewOptInt(*sc.MonthDay)
	}
	if sc.LastRunAt != nil {
		out.LastRunAt = gen.NewOptDateTime(*sc.LastRunAt)
	}
	return out
}

func fromScheduleInput(req *gen.ReportScheduleInput) report.ScheduleInput {
	in := report.ScheduleInput{
		Template: req.Template, Format: string(req.Format), Params: req.Params.Or(nil), Title: req.Title.Or(""),
		Frequency: string(req.Frequency), Hour: req.Hour, Recipients: req.Recipients, Enabled: req.Enabled.Or(true),
	}
	if v, ok := req.Weekday.Get(); ok {
		in.Weekday = &v
	}
	if v, ok := req.MonthDay.Get(); ok {
		in.MonthDay = &v
	}
	return in
}

func (s *Server) ListReportSchedules(ctx context.Context) ([]gen.ReportSchedule, error) {
	list, err := s.reports.Schedules(ctx, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	out := make([]gen.ReportSchedule, len(list))
	for i := range list {
		out[i] = *toSchedule(&list[i])
	}
	return out, nil
}

func (s *Server) CreateReportSchedule(ctx context.Context, req *gen.ReportScheduleInput) (*gen.ReportSchedule, error) {
	p := principal(ctx)
	if err := checkTemplate(p, req.Template); err != nil {
		return nil, err
	}
	sc, err := s.reports.SaveSchedule(ctx, p.UserID, nil, fromScheduleInput(req))
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "report_schedule.created", EntityType: "report_schedule", EntityID: sc.ID.String(),
		Details: map[string]any{"template": sc.Template, "frequency": sc.Frequency, "recipients": sc.Recipients},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	return toSchedule(sc), nil
}

func (s *Server) UpdateReportSchedule(ctx context.Context, req *gen.ReportScheduleInput, params gen.UpdateReportScheduleParams) (*gen.ReportSchedule, error) {
	p := principal(ctx)
	if err := checkTemplate(p, req.Template); err != nil {
		return nil, err
	}
	sc, err := s.reports.SaveSchedule(ctx, p.UserID, &params.ScheduleId, fromScheduleInput(req))
	if err != nil {
		return nil, err
	}
	return toSchedule(sc), nil
}

func (s *Server) DeleteReportSchedule(ctx context.Context, params gen.DeleteReportScheduleParams) error {
	return s.reports.DeleteSchedule(ctx, principal(ctx).UserID, params.ScheduleId)
}

// reportProblem переводит ошибки отчётов в ответы API.
func reportProblem(err error) (status int, code, title string, ok bool) {
	var inv *report.ErrInvalid
	if errors.As(err, &inv) {
		return http.StatusUnprocessableEntity, "invalid_report", inv.Reason, true
	}
	return 0, "", "", false
}

// reportData — сводка модуля отчётов как объект JSON.
func reportData(v any, err error) (gen.ReportData, error) {
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	obj, err := rawObject(b)
	return gen.ReportData(obj), err
}

func (s *Server) ReportPassability(ctx context.Context, params gen.ReportPassabilityParams) (gen.ReportData, error) {
	var tid *uuid.UUID
	if v, ok := params.TerritoryId.Get(); ok {
		tid = &v
	}
	return reportData(s.reports.PassabilityData(ctx, tid, params.Profile))
}

func (s *Server) ReportRoadworks(ctx context.Context, params gen.ReportRoadworksParams) (gen.ReportData, error) {
	var tid *uuid.UUID
	if v, ok := params.TerritoryId.Get(); ok {
		tid = &v
	}
	return reportData(s.reports.RoadworksData(ctx, tid, params.Days.Or(14)))
}

func (s *Server) ReportQuality(ctx context.Context) (gen.ReportData, error) {
	return reportData(s.reports.QualityData(ctx))
}

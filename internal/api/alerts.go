package api

import (
	"context"
	"errors"
	"net/http"

	"freightflow/internal/alerting"
	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
)

// alertActor — субъект для модуля алертов: оператор видит и меняет все алерты.
func alertActor(ctx context.Context) alerting.Actor {
	p := principal(ctx)
	return alerting.Actor{UserID: p.UserID, Manage: p.Has(identity.PermAlertsManage)}
}

func toAlertRule(r *alerting.Rule) (gen.AlertRule, error) {
	var out gen.AlertRule
	return out, recode(&out, r)
}

func fromAlertRuleInput(req *gen.AlertRuleInput) (alerting.RuleInput, error) {
	var in alerting.RuleInput
	if err := recode(&in, req); err != nil {
		return in, err
	}
	in.Enabled = req.Enabled.Or(true)
	return in, nil
}

func (s *Server) ListAlertRules(ctx context.Context, params gen.ListAlertRulesParams) ([]gen.AlertRule, error) {
	list, err := s.alerts.ListRules(ctx, alertActor(ctx), params.All.Or(false))
	if err != nil {
		return nil, err
	}
	out := make([]gen.AlertRule, len(list))
	for i := range list {
		if out[i], err = toAlertRule(&list[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Server) CreateAlertRule(ctx context.Context, req *gen.AlertRuleInput) (*gen.AlertRule, error) {
	in, err := fromAlertRuleInput(req)
	if err != nil {
		return nil, err
	}
	r, err := s.alerts.CreateRule(ctx, alertActor(ctx), in)
	if err != nil {
		return nil, err
	}
	p := principal(ctx)
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "alert_rule.created", EntityType: "alert_rule", EntityID: r.ID.String(),
		Details: map[string]any{"name": r.Name, "kind": r.Kind, "channels": r.Channels},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out, err := toAlertRule(r)
	return &out, err
}

func (s *Server) GetAlertRule(ctx context.Context, params gen.GetAlertRuleParams) (*gen.AlertRule, error) {
	r, err := s.alerts.Rule(ctx, alertActor(ctx), params.RuleId)
	if err != nil {
		return nil, err
	}
	out, err := toAlertRule(r)
	return &out, err
}

func (s *Server) UpdateAlertRule(ctx context.Context, req *gen.AlertRuleInput, params gen.UpdateAlertRuleParams) (*gen.AlertRule, error) {
	in, err := fromAlertRuleInput(req)
	if err != nil {
		return nil, err
	}
	r, err := s.alerts.UpdateRule(ctx, alertActor(ctx), params.RuleId, in)
	if err != nil {
		return nil, err
	}
	p := principal(ctx)
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "alert_rule.updated", EntityType: "alert_rule", EntityID: r.ID.String(),
		Details: map[string]any{"name": r.Name, "enabled": r.Enabled, "channels": r.Channels},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out, err := toAlertRule(r)
	return &out, err
}

func (s *Server) DeleteAlertRule(ctx context.Context, params gen.DeleteAlertRuleParams) error {
	if err := s.alerts.DeleteRule(ctx, alertActor(ctx), params.RuleId); err != nil {
		return err
	}
	p := principal(ctx)
	return s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "alert_rule.deleted", EntityType: "alert_rule", EntityID: params.RuleId.String(),
	}, meta(ctx))
}

func (s *Server) CheckAlertRule(ctx context.Context, params gen.CheckAlertRuleParams) (*gen.AlertRuleCheck, error) {
	a := alertActor(ctx)
	r, err := s.alerts.Rule(ctx, a, params.RuleId)
	if err != nil {
		return nil, err
	}
	if r.OwnerID != a.UserID {
		return nil, alerting.ErrNotFound
	}
	hits, err := s.alerts.CheckRule(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if r, err = s.alerts.Rule(ctx, a, params.RuleId); err != nil {
		return nil, err
	}
	rule, err := toAlertRule(r)
	return &gen.AlertRuleCheck{Rule: rule, Hits: hits}, err
}

func (s *Server) AlertRuleOptions(ctx context.Context) (*gen.AlertRuleOptions, error) {
	c := alerting.Catalog()
	opts := func(list []alerting.Option) []gen.AlertOption {
		out := make([]gen.AlertOption, len(list))
		for i, o := range list {
			out[i] = gen.AlertOption{Code: o.Code, Title: o.Title}
		}
		return out
	}
	out := &gen.AlertRuleOptions{
		WorkKinds: opts(c.WorkKinds), Changes: opts(c.Changes), RestrictionKinds: opts(c.RestrictionKinds),
		Targets: opts(c.Targets), FacilityTypes: opts(c.FacilityTypes),
	}
	for _, k := range c.Kinds {
		ko := gen.AlertKindOption{Code: gen.AlertRuleKind(k.Code), Title: k.Title, Description: k.Description}
		for _, sc := range k.Scopes {
			ko.Scopes = append(ko.Scopes, gen.AlertKindOptionScopesItem(sc))
		}
		out.Kinds = append(out.Kinds, ko)
	}
	profiles, err := s.routing.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		out.Profiles = append(out.Profiles, gen.AlertOption{Code: p.Code, Title: p.Name})
	}
	sources, err := s.ingest.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		out.Sources = append(out.Sources, gen.AlertOption{Code: src.Code, Title: src.Title})
	}
	return out, nil
}

func toAlert(a *alerting.Alert) (gen.Alert, error) {
	var out gen.Alert
	return out, recode(&out, a)
}

func (s *Server) ListAlerts(ctx context.Context, params gen.ListAlertsParams) (*gen.AlertPage, error) {
	f := alerting.Filter{
		Kind: string(params.Kind.Or("")), Assigned: params.Assigned.Or(false), Query: params.Q.Or(""),
		Limit: params.Limit.Or(50), Cursor: params.Cursor.Or(""),
	}
	for _, st := range params.Status {
		f.Status = append(f.Status, string(st))
	}
	for _, sv := range params.Severity {
		f.Severity = append(f.Severity, string(sv))
	}
	if id, ok := params.RuleId.Get(); ok {
		f.RuleID = &id
	}
	page, err := s.alerts.List(ctx, alertActor(ctx), f)
	if err != nil {
		return nil, err
	}
	out := &gen.AlertPage{Items: make([]gen.Alert, len(page.Items))}
	for i := range page.Items {
		if out.Items[i], err = toAlert(&page.Items[i]); err != nil {
			return nil, err
		}
	}
	if page.Next != "" {
		out.Next = gen.NewOptString(page.Next)
	}
	return out, nil
}

func (s *Server) AlertSummary(ctx context.Context) (*gen.AlertSummary, error) {
	sum, err := s.alerts.Summary(ctx, alertActor(ctx))
	if err != nil {
		return nil, err
	}
	return &gen.AlertSummary{Active: sum.Active, Acknowledged: sum.Acknowledged, BySeverity: sum.BySeverity}, nil
}

func (s *Server) ListAlertAssignees(ctx context.Context) ([]gen.AlertUser, error) {
	users, err := s.identity.UsersWithPermission(ctx, identity.PermAlertsOwn, identity.PermAlertsManage)
	if err != nil {
		return nil, err
	}
	out := make([]gen.AlertUser, len(users))
	for i, u := range users {
		out[i] = gen.AlertUser{ID: u.ID, Name: u.Name}
	}
	return out, nil
}

// alertDetail — алерт с журналом, правом на изменение и областью правила.
func (s *Server) alertDetail(ctx context.Context, a alerting.Actor, al *alerting.Alert) (*gen.AlertDetail, error) {
	log, err := s.alerts.Log(ctx, a, al.ID)
	if err != nil {
		return nil, err
	}
	out := &gen.AlertDetail{CanManage: a.Manage || al.OwnerID == a.UserID, Log: []gen.AlertLogEntry{}}
	if out.Alert, err = toAlert(al); err != nil {
		return nil, err
	}
	if err := recode(&out.Log, log); err != nil {
		return nil, err
	}
	area, err := s.alerts.RuleArea(ctx, al.RuleID)
	if err != nil {
		return nil, err
	}
	if area != nil {
		obj, err := rawObject(area)
		if err != nil {
			return nil, err
		}
		out.RuleArea = gen.NewOptAlertDetailRuleArea(obj)
	}
	return out, nil
}

func (s *Server) GetAlert(ctx context.Context, params gen.GetAlertParams) (*gen.AlertDetail, error) {
	a := alertActor(ctx)
	al, err := s.alerts.Get(ctx, a, params.AlertId)
	if err != nil {
		return nil, err
	}
	return s.alertDetail(ctx, a, al)
}

func (s *Server) ActOnAlert(ctx context.Context, req *gen.AlertAction, params gen.ActOnAlertParams) (*gen.AlertDetail, error) {
	a := alertActor(ctx)
	c := alerting.Change{Action: string(req.Action), Comment: req.Comment.Or("")}
	if id, ok := req.AssigneeId.Get(); ok {
		c.Assignee = &id
	}
	al, err := s.alerts.Act(ctx, a, params.AlertId, c)
	if err != nil {
		return nil, err
	}
	if c.Action != alerting.ActionComment {
		p := principal(ctx)
		details := map[string]any{"action": c.Action}
		if c.Assignee != nil {
			details["assigneeId"] = *c.Assignee
		}
		if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
			Action: "alert." + c.Action, EntityType: "alert", EntityID: al.ID.String(), Details: details,
		}, meta(ctx)); err != nil {
			return nil, err
		}
	}
	return s.alertDetail(ctx, a, al)
}

// alertProblem переводит ошибки модуля алертов в ответы API.
func alertProblem(err error) (status int, code, title string, ok bool) {
	var inv *alerting.ErrInvalid
	switch {
	case errors.As(err, &inv):
		return http.StatusUnprocessableEntity, "invalid_alert", inv.Reason, true
	case errors.Is(err, alerting.ErrNotFound):
		return http.StatusNotFound, "alert_not_found", "Не найдено", true
	case errors.Is(err, alerting.ErrForbidden):
		return http.StatusForbidden, "alert_forbidden", "Менять статус может владелец правила или оператор", true
	}
	return 0, "", "", false
}

package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-faster/jx"

	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/ingest"
)

// --- Источники данных и загрузки ---

func (s *Server) ListSources(ctx context.Context) ([]gen.SourceInfo, error) {
	list, err := s.ingest.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.SourceInfo, len(list))
	now := time.Now()
	for i, st := range list {
		out[i] = toSourceInfo(st, now)
	}
	return out, nil
}

func (s *Server) UpdateSource(ctx context.Context, req *gen.UpdateSource, params gen.UpdateSourceParams) (*gen.SourceInfo, error) {
	st, err := s.ingest.SetEnabled(ctx, params.Code, req.Enabled)
	if err != nil {
		return nil, err
	}
	p := principal(ctx)
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "data.source_updated", EntityType: "source", EntityID: params.Code,
		Details: map[string]any{"enabled": req.Enabled},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	info := toSourceInfo(st, time.Now())
	return &info, nil
}

func (s *Server) ListRuns(ctx context.Context, params gen.ListRunsParams) ([]gen.Run, error) {
	if _, err := s.ingest.Source(ctx, params.Code); err != nil {
		return nil, err
	}
	var before *time.Time
	if v, ok := params.Before.Get(); ok {
		before = &v
	}
	runs, err := s.ingest.ListRuns(ctx, params.Code, before, params.Limit.Or(20))
	if err != nil {
		return nil, err
	}
	out := make([]gen.Run, len(runs))
	for i, r := range runs {
		out[i] = toRun(r)
	}
	return out, nil
}

func (s *Server) StartRun(ctx context.Context, req *gen.StartRun, params gen.StartRunParams) (*gen.Run, error) {
	p := principal(ctx)
	force := req.Force.Or(false)
	run, err := s.ingest.Enqueue(ctx, params.Code, ingest.TriggerManual, force, &p.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "data.run_started", EntityType: "source", EntityID: params.Code,
		Details: map[string]any{"run": run.ID, "force": force},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	out := toRun(run)
	return &out, nil
}

func (s *Server) GetRun(ctx context.Context, params gen.GetRunParams) (*gen.RunDetail, error) {
	run, err := s.ingest.GetRun(ctx, params.RunId)
	if err != nil {
		return nil, err
	}
	checks, err := s.ingest.Checks(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	d := &gen.RunDetail{Run: toRun(run), Log: make([]gen.RunLogEntry, len(run.Log)), Checks: make([]gen.QualityCheck, len(checks))}
	for i, l := range run.Log {
		d.Log[i] = gen.RunLogEntry{At: l.At, Level: gen.RunLogEntryLevel(l.Level), Message: l.Message}
	}
	for i, c := range checks {
		qc := gen.QualityCheck{
			Code: c.Code, Title: c.Title, Severity: gen.QualityCheckSeverity(c.Severity), Passed: c.Passed,
			Observed: rawJSON(c.Observed), Expected: rawJSON(c.Expected), CheckedAt: c.CheckedAt,
		}
		if c.Message != "" {
			qc.Message = gen.NewOptString(c.Message)
		}
		d.Checks[i] = qc
	}
	return d, nil
}

func toSourceInfo(st ingest.SourceStatus, now time.Time) gen.SourceInfo {
	info := gen.SourceInfo{
		Code: st.Code, Title: st.Title, Publisher: st.Publisher, URL: st.URL, License: st.License,
		Attribution: st.Attribution, Domains: st.Domains, RuNetwork: st.RuNetwork, Enabled: st.Enabled,
		Freshness: gen.SourceInfoFreshness(freshness(st, now)),
	}
	if st.Interval > 0 {
		info.IntervalSeconds = gen.NewOptInt(int(st.Interval / time.Second))
	}
	if st.LastRun != nil {
		info.LastRun = gen.NewOptRun(toRun(*st.LastRun))
	}
	if st.LastSuccessAt != nil {
		info.LastSuccessAt = gen.NewOptDateTime(*st.LastSuccessAt)
	}
	if st.RecordCount != nil {
		info.RecordCount = gen.NewOptInt64(*st.RecordCount)
	}
	return info
}

// freshness: данные устарели, если пропущено больше двух плановых загрузок.
func freshness(st ingest.SourceStatus, now time.Time) string {
	switch {
	case st.LastSuccessAt == nil:
		return "never"
	case st.Interval > 0 && now.Sub(*st.LastSuccessAt) > 2*st.Interval:
		return "stale"
	default:
		return "fresh"
	}
}

func toRun(r ingest.Run) gen.Run {
	out := gen.Run{
		ID: r.ID, Source: r.Source, Status: gen.RunStatus(r.Status), Trigger: gen.RunTrigger(r.Trigger),
		Forced: r.Forced, QueuedAt: r.QueuedAt, Stats: gen.RunStats(r.Stats),
	}
	if out.Stats == nil {
		out.Stats = gen.RunStats{}
	}
	if r.StartedAt != nil {
		out.StartedAt = gen.NewOptDateTime(*r.StartedAt)
	}
	if r.FinishedAt != nil {
		out.FinishedAt = gen.NewOptDateTime(*r.FinishedAt)
	}
	if r.Error != "" {
		out.Error = gen.NewOptString(r.Error)
	}
	return out
}

func rawJSON(v any) jx.Raw {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return jx.Raw(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return jx.Raw(b)
}

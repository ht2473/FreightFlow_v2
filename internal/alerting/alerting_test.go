package alerting_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/alerting"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/jobs"
)

// env — база с владельцем правил, вторым аналитиком и оператором.
type env struct {
	pool             *pgxpool.Pool
	svc              *alerting.Service
	owner, other, op alerting.Actor
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := dbtest.New(t)
	e := &env{
		pool:  pool,
		svc:   alerting.New(pool, nil, slog.New(slog.DiscardHandler)),
		owner: alerting.Actor{UserID: uuid.New()},
		other: alerting.Actor{UserID: uuid.New()},
		op:    alerting.Actor{UserID: uuid.New(), Manage: true},
	}
	_, err := pool.Exec(t.Context(), `INSERT INTO core.app_user (id, email, name, status) VALUES
		($1, 'owner@example.com', 'Владелец', 'active'), ($2, 'other@example.com', 'Другой', 'active'),
		($3, 'op@example.com', 'Оператор', 'active')`, e.owner.UserID, e.other.UserID, e.op.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (e *env) check(t *testing.T, r *alerting.Rule) int {
	t.Helper()
	n, err := e.svc.CheckRule(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) alerts(t *testing.T, a alerting.Actor, f alerting.Filter) []alerting.Alert {
	t.Helper()
	page, err := e.svc.List(t.Context(), a, f)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func (e *env) rule(t *testing.T, in alerting.RuleInput) *alerting.Rule {
	t.Helper()
	in.Enabled = true
	r, err := e.svc.CreateRule(t.Context(), e.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Коридор вдоль Варварки (условно), работа на нём и работа в стороне.
const corridor = `{"type":"LineString","coordinates":[[37.620,55.755],[37.640,55.757]]}`

func addWork(t *testing.T, e *env, id uuid.UUID, gid int, kind string, lon, lat float64) {
	t.Helper()
	e.exec(t, `INSERT INTO operations.road_work (id, mos_dataset, mos_global_id, kind, title, place, closure,
			planned_start, planned_end, geom, state, content_hash, source_code)
		VALUES ($1, 1, $2, $3, 'Ремонт теплосети', 'улица Варварка, 10', $3 = 'closure', '2026-11-01', '2026-11-20',
			ST_SetSRID(ST_MakePoint($4, $5), 4326), 'planned', 'h', 'mos_works_emergency')`, id, gid, kind, lon, lat)
	e.exec(t, `INSERT INTO operations.road_work_event (work_id, type) VALUES ($1, 'appeared')`, id)
}

func TestRoadworkRuleDedupRepeatAndResolve(t *testing.T) {
	t.Parallel()
	e := setup(t)
	r := e.rule(t, alerting.RuleInput{
		Name: "Варварка", Kind: alerting.KindRoadworks,
		Scope: alerting.Scope{Type: alerting.ScopeCorridor, Line: json.RawMessage(corridor), BufferM: 200},
	})
	if r.Area == nil || r.Severity != alerting.SeverityWarning || len(r.Params.Changes) != 2 {
		t.Fatalf("rule defaults: %+v", r)
	}

	inside, outside := uuid.New(), uuid.New()
	addWork(t, e, inside, 1, "emergency", 37.630, 55.756)
	addWork(t, e, outside, 2, "repair", 37.700, 55.800)
	if n := e.check(t, r); n != 1 {
		t.Fatalf("hits = %d, want 1 (work outside corridor ignored)", n)
	}
	if n := e.check(t, r); n != 0 {
		t.Fatalf("second check hits = %d, want 0 (dedup)", n)
	}
	list := e.alerts(t, e.owner, alerting.Filter{})
	if len(list) != 1 {
		t.Fatalf("alerts = %d", len(list))
	}
	a := list[0]
	if a.Severity != alerting.SeverityCritical || !strings.HasPrefix(a.Title, "Аварийная работа (новая)") ||
		a.Entity != "road_work:"+inside.String() || a.Geometry == nil {
		t.Fatalf("alert: %+v", a)
	}
	if !strings.Contains(a.Summary, "улица Варварка, 10") || !strings.Contains(a.Summary, "01.11.2026–20.11.2026") {
		t.Fatalf("summary = %q", a.Summary)
	}

	// Начало работы — повтор того же алерта, изменения без подписки — нет.
	e.exec(t, `INSERT INTO operations.road_work_event (work_id, type) VALUES ($1, 'changed'), ($1, 'started')`, inside)
	if n := e.check(t, r); n != 1 {
		t.Fatalf("hits after start = %d, want 1", n)
	}
	a = e.alerts(t, e.owner, alerting.Filter{})[0]
	if a.Occurrences != 2 || !strings.Contains(a.Title, "(началась)") {
		t.Fatalf("repeat: occurrences %d, title %q", a.Occurrences, a.Title)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM notify.outbox WHERE type IN ('alert.created', 'alert.updated')`); n != 2 {
		t.Fatalf("alert events = %d, want 2", n)
	}

	// Завершённая работа закрывает алерт автоматически.
	e.exec(t, `UPDATE operations.road_work SET state = 'finished' WHERE id = $1`, inside)
	e.check(t, r)
	a = e.alerts(t, e.owner, alerting.Filter{})[0]
	if a.Status != alerting.StatusClosed || a.Resolution == nil || *a.Resolution != "auto" {
		t.Fatalf("after finish: status %s, resolution %v", a.Status, a.Resolution)
	}
}

func TestAlertLifecycleAndAccess(t *testing.T) {
	t.Parallel()
	e := setup(t)
	r := e.rule(t, alerting.RuleInput{
		Name: "Варварка", Kind: alerting.KindRoadworks,
		Scope: alerting.Scope{Type: alerting.ScopeCorridor, Line: json.RawMessage(corridor)},
	})
	addWork(t, e, uuid.New(), 1, "repair", 37.630, 55.756)
	e.check(t, r)
	id := e.alerts(t, e.owner, alerting.Filter{})[0].ID
	ctx := t.Context()

	// Чужой аналитик алерта не видит; оператор видит все.
	if got := e.alerts(t, e.other, alerting.Filter{}); len(got) != 0 {
		t.Fatalf("other sees %d alerts", len(got))
	}
	if _, err := e.svc.Act(ctx, e.other, id, alerting.Change{Action: alerting.ActionAcknowledge}); !errors.Is(err, alerting.ErrNotFound) {
		t.Fatalf("other acknowledge: %v", err)
	}
	if got := e.alerts(t, e.op, alerting.Filter{Status: []string{alerting.StatusActive}}); len(got) != 1 {
		t.Fatalf("operator sees %d active", len(got))
	}

	steps := []struct {
		who    alerting.Actor
		change alerting.Change
		status string
	}{
		{e.owner, alerting.Change{Action: alerting.ActionAcknowledge, Comment: "Смотрю"}, alerting.StatusAcknowledged},
		{e.op, alerting.Change{Action: alerting.ActionAssign, Assignee: &e.other.UserID}, alerting.StatusAcknowledged},
		{e.other, alerting.Change{Action: alerting.ActionComment, Comment: "Объезд по Ильинке"}, alerting.StatusAcknowledged},
		{e.op, alerting.Change{Action: alerting.ActionClose}, alerting.StatusClosed},
		{e.owner, alerting.Change{Action: alerting.ActionReopen}, alerting.StatusActive},
	}
	for i, s := range steps {
		a, err := e.svc.Act(ctx, s.who, id, s.change)
		if err != nil {
			t.Fatalf("step %d %s: %v", i, s.change.Action, err)
		}
		if a.Status != s.status {
			t.Fatalf("step %d: status %s, want %s", i, a.Status, s.status)
		}
	}
	// Назначенный видит алерт и комментирует, но статус не меняет.
	if _, err := e.svc.Act(ctx, e.other, id, alerting.Change{Action: alerting.ActionClose}); !errors.Is(err, alerting.ErrForbidden) {
		t.Fatalf("assignee close: %v", err)
	}
	if got := e.alerts(t, e.other, alerting.Filter{Assigned: true}); len(got) != 1 {
		t.Fatalf("assigned to other: %d", len(got))
	}
	if _, err := e.svc.Act(ctx, e.owner, id, alerting.Change{Action: alerting.ActionAcknowledge}); err != nil {
		t.Fatal(err)
	}
	var invalid *alerting.ErrInvalid
	if _, err := e.svc.Act(ctx, e.owner, id, alerting.Change{Action: alerting.ActionAcknowledge}); !errors.As(err, &invalid) {
		t.Fatalf("double acknowledge: %v", err)
	}

	log, err := e.svc.Log(ctx, e.owner, id)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, l := range log {
		actions = append(actions, l.Action)
	}
	want := "created acknowledged assigned commented closed reopened acknowledged"
	if got := strings.Join(actions, " "); got != want {
		t.Fatalf("log = %s, want %s", got, want)
	}
	if log[1].Comment == nil || *log[1].Comment != "Смотрю" || log[1].ActorName == nil || *log[1].ActorName != "Владелец" {
		t.Fatalf("log entry: %+v", log[1])
	}
	sum, err := e.svc.Summary(ctx, e.op)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Acknowledged != 1 || sum.Active != 0 {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestRestrictionRuleComparesWithPreviousCheck(t *testing.T) {
	t.Parallel()
	e := setup(t)
	// Участок пути OSM 201 с ограничением массы; load повторяет перезагрузку сети: новые номера
	// участка и ограничения при тех же пути и значении.
	load := func(value float64) {
		e.exec(t, `DELETE FROM network.restriction`)
		e.exec(t, `DELETE FROM network.road_segment`)
		e.exec(t, `INSERT INTO network.road_segment (id, osm_id, class, name, length_m, geom, source_code)
			VALUES (uuidv7(), 201, 'primary', 'улица Варварка', 600,
				ST_SetSRID(ST_MakeLine(ST_MakePoint(37.626, 55.7555), ST_MakePoint(37.632, 55.7565)), 4326), 'osm')`)
		e.exec(t, `INSERT INTO network.restriction (id, segment_id, kind, value, unit, raw, origin, source_code)
			SELECT uuidv7(), id, 'maxweight', $1::numeric, 't', $1::numeric::text, 'osm', 'osm' FROM network.road_segment`, value)
	}
	load(12)
	area := `{"type":"Polygon","coordinates":[[[37.62,55.75],[37.64,55.75],[37.64,55.76],[37.62,55.76],[37.62,55.75]]]}`
	r := e.rule(t, alerting.RuleInput{
		Name: "Китай-город", Kind: alerting.KindRestrictions,
		Scope: alerting.Scope{Type: alerting.ScopeArea, Polygon: json.RawMessage(area)},
	})
	if n := e.check(t, r); n != 0 {
		t.Fatalf("first check hits = %d, want 0 (state only)", n)
	}
	// Перезагрузка сети с новыми номерами без изменений — не изменение.
	load(12)
	if n := e.check(t, r); n != 0 {
		t.Fatalf("renumbered segments hits = %d, want 0", n)
	}
	load(8)
	if n := e.check(t, r); n != 1 {
		t.Fatalf("hits after change = %d, want 1", n)
	}
	a := e.alerts(t, e.owner, alerting.Filter{})[0]
	var d struct {
		Changes []struct{ Op, Kind, From, To, Name string }
	}
	if err := json.Unmarshal(a.Details, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Changes) != 1 || d.Changes[0].Op != "changed" || d.Changes[0].From != "12" || d.Changes[0].To != "8" ||
		d.Changes[0].Name != "улица Варварка" {
		t.Fatalf("changes: %+v", d.Changes)
	}
	e.exec(t, `DELETE FROM network.restriction`)
	if n := e.check(t, r); n != 1 {
		t.Fatalf("hits after removal = %d", n)
	}
	if a = e.alerts(t, e.owner, alerting.Filter{})[0]; a.Occurrences != 2 || !strings.Contains(a.Summary, "снятых — 1") {
		t.Fatalf("removal: %+v", a)
	}
}

func TestAccessibilityRule(t *testing.T) {
	t.Parallel()
	e := setup(t)
	fid := uuid.New()
	e.exec(t, `INSERT INTO infra.facility (id, type_code, name, point, confidence, osm_type, osm_id, source_code)
		VALUES ($1, 'warehouse', 'Склад на Варварке', ST_SetSRID(ST_MakePoint(37.629, 55.756), 4326), 0.8, 'W', 101, 'osm')`, fid)
	e.exec(t, `INSERT INTO analytics.accessibility (facility_id, profile_code, target_kind, minutes, km, graph_built_at)
		VALUES ($1, 'truck_12', 'mkad', 10, 8, now())`, fid)
	r := e.rule(t, alerting.RuleInput{
		Name: "Склад", Kind: alerting.KindAccessibility,
		Scope:  alerting.Scope{Type: alerting.ScopeFacilities, FacilityIDs: []uuid.UUID{fid}},
		Params: alerting.Params{Profile: "truck_12"},
	})
	e.check(t, r)
	e.exec(t, `UPDATE analytics.accessibility SET minutes = 13`)
	if n := e.check(t, r); n != 0 {
		t.Fatalf("below threshold hits = %d", n)
	}
	e.exec(t, `UPDATE analytics.accessibility SET minutes = 19.5`)
	if n := e.check(t, r); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}
	a := e.alerts(t, e.owner, alerting.Filter{})[0]
	if a.Title != "Склад на Варварке: время до МКАД выросло на 6,5 мин" || a.Summary != "13,0 → 19,5 мин, профиль truck_12" {
		t.Fatalf("alert: %q / %q", a.Title, a.Summary)
	}
	e.exec(t, `DELETE FROM analytics.accessibility`)
	e.check(t, r)
	if a = e.alerts(t, e.owner, alerting.Filter{})[0]; a.Occurrences != 2 || a.Title != "Склад на Варварке: недоступен от МКАД" {
		t.Fatalf("unreachable: %+v", a)
	}
}

func TestFacilityAndSourceRules(t *testing.T) {
	t.Parallel()
	e := setup(t)
	facilities := e.rule(t, alerting.RuleInput{
		Name: "Новые склады", Kind: alerting.KindFacilities,
		Scope: alerting.Scope{Type: alerting.ScopeCity}, Params: alerting.Params{FacilityTypes: []string{"warehouse"}},
	})
	e.exec(t, `INSERT INTO infra.facility (id, type_code, name, point, confidence, osm_type, osm_id, source_code, first_seen_at) VALUES
		(uuidv7(), 'warehouse', 'Новый склад', ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 0.8, 'W', 1, 'osm', now()),
		(uuidv7(), 'terminal', 'Новый терминал', ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 0.8, 'W', 2, 'osm', now()),
		(uuidv7(), 'warehouse', 'Старый склад', ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 0.8, 'W', 3, 'osm', now() - interval '1 day')`)
	if n := e.check(t, facilities); n != 1 {
		t.Fatalf("new facility hits = %d, want 1", n)
	}

	e.exec(t, `INSERT INTO ingest.source (code, title, publisher, url, license, attribution, domains, interval_s)
		VALUES ('test_works', 'Работы (тест)', 'тест', 'https://example.com', 'CC BY 4.0', 'тест', '{operations}', 86400)`)
	sources := e.rule(t, alerting.RuleInput{Name: "Источники", Kind: alerting.KindSources})
	e.exec(t, `INSERT INTO ingest.run (id, source_code, status, trigger, finished_at, error)
		VALUES (uuidv7(), 'test_works', 'failed', 'schedule', now(), 'портал ответил 503')`)
	if n := e.check(t, sources); n != 1 {
		t.Fatalf("failed run hits = %d", n)
	}
	a := e.alerts(t, e.owner, alerting.Filter{Kind: alerting.KindSources})[0]
	if a.Title != "Сбой загрузки: Работы (тест)" || a.Summary != "портал ответил 503" {
		t.Fatalf("source alert: %+v", a)
	}
	e.exec(t, `INSERT INTO ingest.run (id, source_code, status, trigger, finished_at)
		VALUES (uuidv7(), 'test_works', 'succeeded', 'schedule', now() + interval '1 second')`)
	e.check(t, sources)
	if a = e.alerts(t, e.owner, alerting.Filter{Kind: alerting.KindSources})[0]; a.Status != alerting.StatusClosed {
		t.Fatalf("source alert not resolved: %s", a.Status)
	}
	// Успешная загрузка трёхдневной давности при суточном интервале — данные устарели.
	e.exec(t, `UPDATE ingest.run SET finished_at = now() - interval '3 days' WHERE status = 'succeeded'`)
	e.exec(t, `UPDATE ingest.run SET finished_at = now() - interval '4 days' WHERE status = 'failed'`)
	if n := e.check(t, sources); n != 1 {
		t.Fatalf("stale hits = %d", n)
	}
	open := e.alerts(t, e.owner, alerting.Filter{Kind: alerting.KindSources, Status: []string{alerting.StatusActive}})
	if len(open) != 1 || !strings.HasPrefix(open[0].Title, "Данные устарели") {
		t.Fatalf("stale alert: %+v", open)
	}
}

func TestRuleValidation(t *testing.T) {
	t.Parallel()
	e := setup(t)
	far := `{"type":"LineString","coordinates":[[30.3,59.9],[30.4,59.95]]}`
	cases := []struct {
		name string
		in   alerting.RuleInput
		want string
	}{
		{"без названия", alerting.RuleInput{Kind: alerting.KindSources}, "название"},
		{"неизвестный вид", alerting.RuleInput{Name: "x", Kind: "weather"}, "неизвестный вид"},
		{"доступность по области", alerting.RuleInput{
			Name: "x", Kind: alerting.KindAccessibility,
			Scope: alerting.Scope{Type: alerting.ScopeArea},
		}, "область задаётся иначе"},
		{"коридор вне охвата", alerting.RuleInput{
			Name: "x", Kind: alerting.KindRoadworks,
			Scope: alerting.Scope{Type: alerting.ScopeCorridor, Line: json.RawMessage(far)},
		}, "вне охвата"},
		{"коридор многоугольником", alerting.RuleInput{
			Name: "x", Kind: alerting.KindRoadworks,
			Scope: alerting.Scope{Type: alerting.ScopeCorridor, Line: json.RawMessage(`{"type":"Point","coordinates":[37.6,55.7]}`)},
		}, "линией"},
		{"сломанный GeoJSON", alerting.RuleInput{
			Name: "x", Kind: alerting.KindRoadworks,
			Scope: alerting.Scope{Type: alerting.ScopeArea, Polygon: json.RawMessage(`{"type":"Polygon"}`)},
		}, "геометрия"},
		{"нет объекта", alerting.RuleInput{
			Name: "x", Kind: alerting.KindAccessibility, Params: alerting.Params{Profile: "truck_12"},
			Scope: alerting.Scope{Type: alerting.ScopeFacilities, FacilityIDs: []uuid.UUID{uuid.New()}},
		}, "не найдена"},
		{"неизвестный профиль", alerting.RuleInput{
			Name: "x", Kind: alerting.KindAccessibility, Params: alerting.Params{Profile: "bike"},
			Scope: alerting.Scope{Type: alerting.ScopeFacilities, FacilityIDs: []uuid.UUID{uuid.New()}},
		}, "не найдена"},
		{"канал", alerting.RuleInput{Name: "x", Kind: alerting.KindSources, Channels: []string{"sms"}}, "каналы"},
		{"порог устаревания", alerting.RuleInput{Name: "x", Kind: alerting.KindSources, Params: alerting.Params{StaleFactor: 1}}, "порог"},
	}
	for _, tc := range cases {
		_, err := e.svc.CreateRule(t.Context(), e.owner, tc.in)
		var inv *alerting.ErrInvalid
		if !errors.As(err, &inv) || !strings.Contains(inv.Reason, tc.want) {
			t.Errorf("%s: err = %v, want reason with %q", tc.name, err, tc.want)
		}
	}

	// Своё правило правит только владелец; вид не меняется.
	r := e.rule(t, alerting.RuleInput{Name: "Источники", Kind: alerting.KindSources})
	upd := alerting.RuleInput{
		Name: "Все источники", Kind: alerting.KindSources, Severity: alerting.SeverityCritical,
		Channels: []string{"email", "in_app", "email"},
	}
	if _, err := e.svc.UpdateRule(t.Context(), e.other, r.ID, upd); !errors.Is(err, alerting.ErrNotFound) {
		t.Fatalf("other update: %v", err)
	}
	got, err := e.svc.UpdateRule(t.Context(), e.owner, r.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Все источники" || strings.Join(got.Channels, ",") != "email,in_app" || got.Enabled {
		t.Fatalf("updated: %+v", got)
	}
	upd.Kind = alerting.KindFacilities
	upd.Scope = alerting.Scope{Type: alerting.ScopeCity}
	var inv *alerting.ErrInvalid
	if _, err := e.svc.UpdateRule(t.Context(), e.owner, r.ID, upd); !errors.As(err, &inv) {
		t.Fatalf("kind change: %v", err)
	}
	if err := e.svc.DeleteRule(t.Context(), e.other, r.ID); !errors.Is(err, alerting.ErrNotFound) {
		t.Fatalf("other delete: %v", err)
	}
	if err := e.svc.DeleteRule(t.Context(), e.owner, r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerQueuesCheckOnDataEvents(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	inserter, err := jobs.NewInserter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	svc := alerting.New(pool, inserter, slog.New(slog.DiscardHandler))
	c := svc.Consumer()
	if err := c.Register(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{events.RoadworkChanged, events.JobProgress, events.DataPublished} {
		err := db.InTx(ctx, pool, func(tx pgx.Tx) error { return events.Publish(ctx, tx, typ, map[string]any{}) })
		if err != nil {
			t.Fatal(err)
		}
	}
	// Порция с двумя событиями-поводами ставит одну проверку; прогресс задач поводом не является.
	deadline := time.Now().Add(15 * time.Second)
	for count(t, pool, `SELECT count(*) FROM queue.river_job WHERE kind = 'alerting.check'`) == 0 {
		if _, err := c.Step(ctx, pool); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("check job was not queued")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := c.Step(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM queue.river_job WHERE kind = 'alerting.check' AND queue = 'alerts'`); n < 1 || n > 2 {
		t.Fatalf("check jobs = %d", n)
	}
}

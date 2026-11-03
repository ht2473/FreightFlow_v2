package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"freightflow/internal/identity"
)

func TestAPIAlerts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	viewer := f.token(t, "av@example.com", identity.RoleViewer)
	analyst := f.token(t, "aa@example.com", identity.RoleAnalyst)
	other := f.token(t, "ab@example.com", identity.RoleAnalyst)
	operator := f.token(t, "ao@example.com", identity.RoleOperator)

	status := func(t *testing.T, method, path, token, body string, want int) []byte {
		t.Helper()
		resp, b := f.do(t, method, path, token, body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, b)
		}
		return b
	}
	status(t, http.MethodGet, "/api/v1/alerts", viewer, "", http.StatusForbidden)
	status(t, http.MethodGet, "/api/v1/alert-rules", viewer, "", http.StatusForbidden)
	status(t, http.MethodGet, "/api/v1/alerts", "", "", http.StatusUnauthorized)

	var opts struct {
		Kinds    []struct{ Code string }
		Profiles []struct{ Code string }
	}
	if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/alert-rules/options", analyst, "", http.StatusOK), &opts); err != nil {
		t.Fatal(err)
	}
	if len(opts.Kinds) != 5 || len(opts.Profiles) == 0 {
		t.Fatalf("options: %+v", opts)
	}

	status(t, http.MethodPost, "/api/v1/alert-rules", analyst,
		`{"name":"x","kind":"accessibility","scope":{"type":"area"}}`, http.StatusUnprocessableEntity)
	rule := `{"name":"Варварка","kind":"roadworks","channels":["in_app"],
		"scope":{"type":"corridor","line":{"type":"LineString","coordinates":[[37.62,55.755],[37.64,55.757]]},"bufferM":150}}`
	var created struct {
		ID       string
		Severity string
		Area     map[string]any
		Params   struct{ Changes []string }
	}
	if err := json.Unmarshal(status(t, http.MethodPost, "/api/v1/alert-rules", analyst, rule, http.StatusCreated), &created); err != nil {
		t.Fatal(err)
	}
	if created.Severity != "warning" || created.Area["type"] != "MultiPolygon" || len(created.Params.Changes) != 2 {
		t.Fatalf("created rule: %+v", created)
	}
	ruleURL := "/api/v1/alert-rules/" + created.ID
	status(t, http.MethodGet, ruleURL, other, "", http.StatusNotFound)
	status(t, http.MethodGet, ruleURL, operator, "", http.StatusOK)
	status(t, http.MethodPut, ruleURL, other, rule, http.StatusNotFound)
	status(t, http.MethodPost, ruleURL+"/check", other, "", http.StatusNotFound)

	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO operations.road_work (id, mos_dataset, mos_global_id, kind, title, closure, geom, content_hash, source_code)
		VALUES (uuidv7(), 1, 1, 'closure', 'Перекрытие Варварки', true, ST_SetSRID(ST_MakePoint(37.63, 55.756), 4326), 'h', 'test');
		INSERT INTO operations.road_work_event (work_id, type) SELECT id, 'appeared' FROM operations.road_work`); err != nil {
		t.Fatal(err)
	}
	var check struct{ Hits int }
	if err := json.Unmarshal(status(t, http.MethodPost, ruleURL+"/check", analyst, "", http.StatusOK), &check); err != nil {
		t.Fatal(err)
	}
	if check.Hits != 1 {
		t.Fatalf("hits = %d", check.Hits)
	}

	type page struct {
		Items []struct {
			ID, Status, Severity, Title string
		}
	}
	list := func(token, query string) page {
		t.Helper()
		var p page
		if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/alerts"+query, token, "", http.StatusOK), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := list(other, ""); len(p.Items) != 0 {
		t.Fatalf("other analyst sees %d alerts", len(p.Items))
	}
	p := list(operator, "?status=active&status=acknowledged&severity=warning&severity=critical&kind=roadworks")
	if len(p.Items) != 1 || p.Items[0].Severity != "warning" {
		t.Fatalf("operator feed: %+v", p)
	}
	alertURL := "/api/v1/alerts/" + p.Items[0].ID
	status(t, http.MethodGet, alertURL, other, "", http.StatusNotFound)
	status(t, http.MethodGet, "/api/v1/alerts?status=lost", operator, "", http.StatusBadRequest)

	var detail struct {
		Alert     struct{ Status string }
		CanManage bool
		Log       []struct{ Action string }
		RuleArea  map[string]any
	}
	if err := json.Unmarshal(status(t, http.MethodPost, alertURL+"/actions", operator,
		`{"action":"acknowledge","comment":"Объезд по Ильинке"}`, http.StatusOK), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Alert.Status != "acknowledged" || !detail.CanManage || len(detail.Log) != 2 || detail.RuleArea == nil {
		t.Fatalf("after acknowledge: %+v", detail)
	}
	status(t, http.MethodPost, alertURL+"/actions", operator, `{"action":"acknowledge"}`, http.StatusUnprocessableEntity)
	status(t, http.MethodPost, alertURL+"/actions", analyst, `{"action":"comment","comment":"Принято"}`, http.StatusOK)
	status(t, http.MethodPost, alertURL+"/actions", analyst, `{"action":"fly"}`, http.StatusBadRequest)

	var users []struct{ Name string }
	if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/alerts/assignees", operator, "", http.StatusOK), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Fatalf("assignees = %d, want analysts and operator", len(users))
	}
	status(t, http.MethodGet, "/api/v1/alerts/assignees", analyst, "", http.StatusForbidden)

	var sum struct{ Active, Acknowledged int }
	if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/alerts/summary", analyst, "", http.StatusOK), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Active != 0 || sum.Acknowledged != 1 {
		t.Fatalf("summary: %+v", sum)
	}

	status(t, http.MethodDelete, ruleURL, other, "", http.StatusNotFound)
	status(t, http.MethodDelete, ruleURL, analyst, "", http.StatusNoContent)
	if p := list(operator, ""); len(p.Items) != 0 {
		t.Fatalf("alerts after rule deletion: %d", len(p.Items))
	}
}

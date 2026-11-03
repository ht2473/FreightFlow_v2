package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"freightflow/internal/identity"
)

func TestAPIReports(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	viewer := f.token(t, "rv@example.com", identity.RoleViewer)
	analyst := f.token(t, "ra@example.com", identity.RoleAnalyst)
	status := func(t *testing.T, method, path, token, body string, want int) []byte {
		t.Helper()
		resp, b := f.do(t, method, path, token, body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, b)
		}
		return b
	}

	// Шаблоны — по правам на их данные: наблюдателю без маршрутов и сценариев.
	var tpls []struct{ Code string }
	if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/report-templates", viewer, "", http.StatusOK), &tpls); err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, x := range tpls {
		codes[x.Code] = true
	}
	if !codes["facility"] || !codes["quality"] || codes["scenario"] {
		t.Fatalf("viewer templates: %v", codes)
	}
	status(t, http.MethodPost, "/api/v1/reports", viewer, `{"template":"quality","format":"pdf"}`, http.StatusForbidden)
	status(t, http.MethodPost, "/api/v1/reports", analyst, `{"template":"nope","format":"pdf"}`, http.StatusUnprocessableEntity)
	status(t, http.MethodPost, "/api/v1/reports", analyst, `{"template":"facility","format":"pdf","params":{}}`, http.StatusUnprocessableEntity)
	var rep struct{ ID, Status string }
	if err := json.Unmarshal(status(t, http.MethodPost, "/api/v1/reports", analyst, `{"template":"quality","format":"xlsx"}`, http.StatusAccepted), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Status != "queued" {
		t.Fatalf("report: %+v", rep)
	}
	status(t, http.MethodGet, "/api/v1/reports/"+rep.ID, analyst, "", http.StatusOK)
	status(t, http.MethodGet, "/api/v1/reports/"+rep.ID+"/file", analyst, "", http.StatusConflict)
	status(t, http.MethodGet, "/api/v1/reports/"+rep.ID, viewer, "", http.StatusNotFound)

	// Расписание.
	status(t, http.MethodPost, "/api/v1/report-schedules", analyst,
		`{"template":"quality","format":"pdf","frequency":"weekly","hour":8}`, http.StatusUnprocessableEntity)
	var sc struct{ ID, NextRunAt string }
	if err := json.Unmarshal(status(t, http.MethodPost, "/api/v1/report-schedules", analyst,
		`{"template":"quality","format":"pdf","frequency":"daily","hour":8,"recipients":["ops@example.com"]}`, http.StatusCreated), &sc); err != nil {
		t.Fatal(err)
	}
	status(t, http.MethodDelete, "/api/v1/report-schedules/"+sc.ID, analyst, "", http.StatusNoContent)

	// Сводки для страниц печати.
	status(t, http.MethodGet, "/api/v1/report-data/quality", viewer, "", http.StatusOK)
	status(t, http.MethodGet, "/api/v1/report-data/roadworks?days=30", viewer, "", http.StatusOK)
	status(t, http.MethodGet, "/api/v1/report-data/passability?profile=truck_20", viewer, "", http.StatusOK)
	status(t, http.MethodGet, "/api/v1/report-data/passability?profile=bike", viewer, "", http.StatusUnprocessableEntity)

	// Токен печати: чтение — да, изменения — нет.
	u, err := f.identity.CreateUser(t.Context(), nil, identity.CreateUserInput{
		Email: "rp@example.com", Name: "Печать", Password: "пароль для тестов 1", Roles: []string{identity.RoleAnalyst},
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	print := f.identity.IssuePrintToken(u.ID, time.Minute)
	status(t, http.MethodGet, "/api/v1/me", print, "", http.StatusOK)
	status(t, http.MethodGet, "/api/v1/report-data/quality", print, "", http.StatusOK)
	status(t, http.MethodPost, "/api/v1/views", print, `{"page":"map","name":"x","params":{}}`, http.StatusForbidden)
	status(t, http.MethodPost, "/api/v1/reports", print, `{"template":"quality","format":"pdf"}`, http.StatusForbidden)
}

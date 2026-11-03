package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"freightflow/internal/identity"
)

func TestAPINotifications(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	viewer := f.token(t, "nv@example.com", identity.RoleViewer)
	analyst := f.token(t, "na@example.com", identity.RoleAnalyst)
	operator := f.token(t, "no@example.com", identity.RoleOperator)
	status := func(t *testing.T, method, path, token, body string, want int) []byte {
		t.Helper()
		resp, b := f.do(t, method, path, token, body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, b)
		}
		return b
	}

	// Центр уведомлений и предпочтения — всем вошедшим; вебхуки — с правом на алерты.
	var page struct {
		Items  []any
		Unread int
	}
	if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/me/notifications", viewer, "", http.StatusOK), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Unread != 0 {
		t.Fatalf("empty center: %+v", page)
	}
	status(t, http.MethodPost, "/api/v1/me/notifications/read", viewer, `{}`, http.StatusUnprocessableEntity)
	status(t, http.MethodPost, "/api/v1/me/notifications/read", viewer, `{"all":true}`, http.StatusNoContent)
	status(t, http.MethodGet, "/api/v1/me/notifications", "", "", http.StatusUnauthorized)
	status(t, http.MethodGet, "/api/v1/me/webhooks", viewer, "", http.StatusForbidden)

	var set struct {
		EmailMode    string
		SubscribeAll bool
		QuietFrom    *int
	}
	body := `{"emailMode":"digest","digestHour":8,"quietFrom":22,"quietTo":7,"minSeverity":"warning","subscribeAll":true,"subscribeSeverity":"critical"}`
	if err := json.Unmarshal(status(t, http.MethodPut, "/api/v1/me/notification-settings", analyst, body, http.StatusOK), &set); err != nil {
		t.Fatal(err)
	}
	if set.EmailMode != "digest" || set.SubscribeAll || set.QuietFrom == nil || *set.QuietFrom != 22 {
		t.Fatalf("analyst settings: %+v", set)
	}
	if err := json.Unmarshal(status(t, http.MethodPut, "/api/v1/me/notification-settings", operator, body, http.StatusOK), &set); err != nil {
		t.Fatal(err)
	}
	if !set.SubscribeAll {
		t.Fatal("operator subscription not saved")
	}
	status(t, http.MethodPut, "/api/v1/me/notification-settings", analyst,
		`{"emailMode":"instant","digestHour":8,"quietFrom":5,"quietTo":5,"minSeverity":"info","subscribeAll":false,"subscribeSeverity":"info"}`,
		http.StatusUnprocessableEntity)
	status(t, http.MethodPut, "/api/v1/me/notification-settings", analyst,
		`{"emailMode":"weekly","digestHour":8,"minSeverity":"info","subscribeAll":false,"subscribeSeverity":"info"}`, http.StatusBadRequest)

	// Telegram без токена бота: статус «не настроен», код не выдаётся.
	var tg struct{ Configured, Bound bool }
	if err := json.Unmarshal(status(t, http.MethodGet, "/api/v1/me/telegram", analyst, "", http.StatusOK), &tg); err != nil {
		t.Fatal(err)
	}
	if tg.Configured || tg.Bound {
		t.Fatalf("telegram: %+v", tg)
	}
	status(t, http.MethodPost, "/api/v1/me/telegram/link", analyst, "", http.StatusConflict)

	// Вебхук: секрет один раз, проверка ping, журнал, чужому недоступен.
	got := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("X-FreightFlow-Signature"), "sha256=") && r.Header.Get("X-FreightFlow-Event") == "ping" {
			got++
		}
	}))
	defer receiver.Close()
	status(t, http.MethodPost, "/api/v1/me/webhooks", analyst, `{"name":"x","url":"ftp://example.com"}`, http.StatusUnprocessableEntity)
	var created struct {
		Webhook struct{ ID string }
		Secret  string
	}
	if err := json.Unmarshal(status(t, http.MethodPost, "/api/v1/me/webhooks", analyst,
		`{"name":"Диспетчерская","url":"`+receiver.URL+`/hook"}`, http.StatusCreated), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("secret = %q", created.Secret)
	}
	hook := "/api/v1/me/webhooks/" + created.Webhook.ID
	list := status(t, http.MethodGet, "/api/v1/me/webhooks", analyst, "", http.StatusOK)
	if strings.Contains(string(list), created.Secret) {
		t.Fatal("secret returned in list")
	}
	var d struct {
		Status       string
		ResponseCode int
	}
	if err := json.Unmarshal(status(t, http.MethodPost, hook+"/test", analyst, "", http.StatusOK), &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "sent" || d.ResponseCode != 200 || got != 1 {
		t.Fatalf("ping: %+v, received %d", d, got)
	}
	status(t, http.MethodGet, hook+"/deliveries", operator, "", http.StatusNotFound)
	status(t, http.MethodPatch, hook, operator, `{"enabled":false}`, http.StatusNotFound)
	status(t, http.MethodPatch, hook, analyst, `{"enabled":false}`, http.StatusNoContent)
	var deliveries []struct{ Event, Status string }
	if err := json.Unmarshal(status(t, http.MethodGet, hook+"/deliveries", analyst, "", http.StatusOK), &deliveries); err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Event != "ping" {
		t.Fatalf("deliveries: %+v", deliveries)
	}
	status(t, http.MethodDelete, hook, operator, "", http.StatusNotFound)
	status(t, http.MethodDelete, hook, analyst, "", http.StatusNoContent)
}

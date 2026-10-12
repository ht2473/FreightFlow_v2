package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	spec "freightflow/api"
	"freightflow/internal/identity"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/problem"
	"freightflow/internal/platform/secret"
	"freightflow/internal/platform/session"
)

func TestEveryOperationHasKnownPermission(t *testing.T) {
	t.Parallel()
	ops, err := Operations(spec.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("no operations found in spec")
	}
	for _, op := range ops {
		switch {
		case op.Permission == "":
			t.Errorf("%s %s (%s): x-permission is missing", op.Method, op.Path, op.ID)
		case op.Permission != PermAuthenticated && !identity.KnownPermission(op.Permission):
			t.Errorf("%s %s (%s): unknown permission %q", op.Method, op.Path, op.ID, op.Permission)
		}
	}
}

type memoryMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *memoryMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

type fixture struct {
	srv      *httptest.Server
	identity *identity.Service
	mailer   *memoryMailer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.New(t)
	box, err := secret.NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	ids := identity.NewService(pool, box)
	if err := ids.SyncBuiltinRoles(context.Background()); err != nil {
		t.Fatal(err)
	}
	mailer := &memoryMailer{}
	sessions := session.New(pool, false)
	h, err := New(Deps{
		Identity: ids, Sessions: sessions, Mailer: mailer, BaseURL: "http://test.local",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpx.Chain(h, httpx.WithClient(), sessions.LoadAndSave))
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, identity: ids, mailer: mailer}
}

func (f *fixture) token(t *testing.T, email string, role string, scopes ...identity.Permission) string {
	t.Helper()
	ctx := context.Background()
	u, err := f.identity.CreateUser(ctx, nil, identity.CreateUserInput{
		Email: email, Name: "Пользователь " + role, Password: "пароль для тестов 1", Roles: []string{role},
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.identity.Principal(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) == 0 {
		scopes = p.Permissions
	}
	plain, _, err := f.identity.CreateAPIToken(ctx, p, "тест", scopes, nil, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func (f *fixture) do(t *testing.T, method, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAPIAuthentication(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	resp, body := f.do(t, http.MethodGet, "/api/v1/me", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no credentials: status %d, body %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content type %q", ct)
	}

	resp, _ = f.do(t, http.MethodGet, "/api/v1/me", "ffv2_wrong", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d", resp.StatusCode)
	}

	tok := f.token(t, "analyst@example.com", identity.RoleAnalyst)
	resp, body = f.do(t, http.MethodGet, "/api/v1/me", tok, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me: status %d, body %s", resp.StatusCode, body)
	}
	var me struct {
		Email       string   `json:"email"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatal(err)
	}
	if me.Email != "analyst@example.com" || len(me.Permissions) == 0 {
		t.Fatalf("unexpected me: %+v", me)
	}
}

func TestAPIAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	analyst := f.token(t, "a@example.com", identity.RoleAnalyst)
	admin := f.token(t, "root@example.com", identity.RoleAdmin)
	narrow := f.token(t, "n@example.com", identity.RoleAdmin, identity.PermMapView)

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		body   string
		want   int
	}{
		{"analyst cannot list users", http.MethodGet, "/api/v1/admin/users", analyst, "", http.StatusForbidden},
		{"admin lists users", http.MethodGet, "/api/v1/admin/users", admin, "", http.StatusOK},
		{"token scope limits admin", http.MethodGet, "/api/v1/admin/users", narrow, "", http.StatusForbidden},
		{"admin reads audit", http.MethodGet, "/api/v1/admin/audit?limit=10", admin, "", http.StatusOK},
		{"tokens require session", http.MethodGet, "/api/v1/me/tokens", analyst, "", http.StatusForbidden},
		{
			"validation error", http.MethodPost, "/api/v1/admin/users", admin,
			`{"email":"bad","name":"X","password":"достаточно длинный","roles":["viewer"]}`, http.StatusUnprocessableEntity,
		},
		{"malformed body", http.MethodPost, "/api/v1/admin/users", admin, `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := f.do(t, tc.method, tc.path, tc.token, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d; body %s", resp.StatusCode, tc.want, body)
			}
		})
	}
}

func TestAPIAdminCreatesUserAndInvites(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	admin := f.token(t, "root@example.com", identity.RoleAdmin)

	resp, body := f.do(t, http.MethodPost, "/api/v1/admin/users", admin,
		`{"email":"New@Example.com","name":"Новый","password":"достаточно длинный пароль","roles":["viewer"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %d %s", resp.StatusCode, body)
	}
	var u struct {
		ID    string   `json:"id"`
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	}
	_ = json.Unmarshal(body, &u)
	if u.Email != "new@example.com" || len(u.Roles) != 1 {
		t.Fatalf("unexpected user %+v", u)
	}

	resp, body = f.do(t, http.MethodPatch, "/api/v1/admin/users/"+u.ID, admin, `{"roles":["analyst","operator"],"status":"blocked"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"blocked"`) {
		t.Fatalf("update user: %d %s", resp.StatusCode, body)
	}

	resp, body = f.do(t, http.MethodPost, "/api/v1/admin/invites", admin, `{"email":"guest@example.com","role":"viewer"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite: %d %s", resp.StatusCode, body)
	}
	if len(f.mailer.sent) != 1 || !strings.Contains(f.mailer.sent[0].Text, "http://test.local/invite/") {
		t.Fatalf("invite mail not sent: %+v", f.mailer.sent)
	}
}

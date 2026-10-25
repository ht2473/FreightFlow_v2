package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	spec "freightflow/api"
	"freightflow/internal/analytics"
	"freightflow/internal/facility"
	"freightflow/internal/identity"
	"freightflow/internal/ingest"
	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/problem"
	"freightflow/internal/platform/secret"
	"freightflow/internal/platform/session"
	"freightflow/internal/regulation"
	"freightflow/internal/report"
	"freightflow/internal/routing"
	"freightflow/internal/search"
	"freightflow/internal/territory"
	"freightflow/internal/tiles"
	"freightflow/internal/views"
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
	pool     *pgxpool.Pool
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
	blobs, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	ing, err := ingest.NewService(ingest.Deps{Pool: pool, Blobs: blobs, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	layers := tiles.NewService(pool, ing, slog.New(slog.NewTextHandler(io.Discard, nil)))
	inserter, err := jobs.NewInserter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	facilities := facility.New(pool)
	h, err := New(Deps{
		Identity: ids, Ingest: ing, Layers: layers, Regulation: regulation.New(pool),
		Routing: routing.NewService(pool, routing.NewClient("http://127.0.0.1:1")), Facilities: facilities,
		Reports: report.New(pool, inserter, blobs, facilities), Views: views.New(pool), Territories: territory.New(pool, facilities), Search: search.New(pool, facilities), Analytics: analytics.New(pool), Sessions: sessions, Mailer: mailer,
		BaseURL: "http://test.local",
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpx.Chain(h, httpx.WithClient(), sessions.LoadAndSave))
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, pool: pool, identity: ids, mailer: mailer}
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
	steward := f.token(t, "s@example.com", identity.RoleDataSteward)

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
		{"analyst cannot manage data", http.MethodGet, "/api/v1/data/sources", analyst, "", http.StatusForbidden},
		{"analyst lists layers", http.MethodGet, "/api/v1/layers", analyst, "", http.StatusOK},
		{"map scope lists layers", http.MethodGet, "/api/v1/layers", narrow, "", http.StatusOK},
		{"steward lists sources", http.MethodGet, "/api/v1/data/sources", steward, "", http.StatusOK},
		{"unknown source", http.MethodPost, "/api/v1/data/sources/nope/runs", steward, `{}`, http.StatusNotFound},
		{"invalid source code", http.MethodGet, "/api/v1/data/sources/Bad-Code/runs", steward, "", http.StatusBadRequest},
		{"unknown run", http.MethodGet, "/api/v1/data/runs/01a13230-0000-7000-8000-000000000000", steward, "", http.StatusNotFound},
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

func TestAPIRegulation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	viewer := f.token(t, "v@example.com", identity.RoleAdmin, identity.PermMapView)

	resp, body := f.do(t, http.MethodGet, "/api/v1/regulation", viewer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var o struct {
		Rules []struct {
			Code, Zone, Effect, Document string
			Confirmed                    bool
			ConfirmedBy                  string
			PassZones                    []string
			TimeFrom, TimeTo             string
		}
		Documents []struct{ Code, Kind, Status string }
		Zones     []any
	}
	if err := json.Unmarshal(body, &o); err != nil {
		t.Fatal(err)
	}
	// Правила и документы — справочные данные миграции; зоны появляются после загрузки OSM.
	docs := map[string]string{}
	for _, d := range o.Documents {
		docs[d.Code] = d.Kind
	}
	if len(o.Rules) == 0 || docs["pp-379-2011"] != "act" || len(o.Zones) != 0 {
		t.Fatalf("overview: %s", body)
	}
	for _, r := range o.Rules {
		if docs[r.Document] == "" || (r.Confirmed && docs[r.ConfirmedBy] == "") {
			t.Errorf("rule %s: unknown document %q / %q", r.Code, r.Document, r.ConfirmedBy)
		}
		if r.Effect == "pass_required" && len(r.PassZones) == 0 {
			t.Errorf("rule %s: no pass zones", r.Code)
		}
		if r.Code == "mkad_gross_night" && (r.TimeFrom != "23:00" || r.TimeTo != "07:00") {
			t.Errorf("night rule time %s–%s", r.TimeFrom, r.TimeTo)
		}
	}

	for _, path := range []string{"/api/v1/regulation/passes", "/api/v1/regulation/permits", "/api/v1/regulation/signs"} {
		if resp, body := f.do(t, http.MethodGet, path, viewer, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d: %s", path, resp.StatusCode, body)
		}
	}
}

func TestAPIRouting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	analyst := f.token(t, "r@example.com", identity.RoleAnalyst)

	resp, body := f.do(t, http.MethodGet, "/api/v1/routing/profiles", analyst, "")
	var profiles []struct {
		Code   string
		GrossT float64
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &profiles) != nil || len(profiles) < 3 {
		t.Fatalf("profiles: %d %s", resp.StatusCode, body)
	}
	// Служба маршрутизации в тесте недоступна: состояние — «недоступна», маршрут — 503.
	if resp, body := f.do(t, http.MethodGet, "/api/v1/routing/status", analyst, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), `"available":false`) {
		t.Errorf("status: %d %s", resp.StatusCode, body)
	}
	req := `{"points":[{"lon":37.6,"lat":55.7},{"lon":37.7,"lat":55.8}],"vehicle":{"grossT":20,"heightM":4,"widthM":2.55,"lengthM":10}}`
	if resp, body := f.do(t, http.MethodPost, "/api/v1/routing/route", analyst, req); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("route: %d %s", resp.StatusCode, body)
	}
	iso := `{"origin":{"lon":37.6,"lat":55.7},"vehicle":{"grossT":20,"heightM":4,"widthM":2.55,"lengthM":10},"minutes":[15,30]}`
	if resp, body := f.do(t, http.MethodPost, "/api/v1/routing/isochrone", analyst, iso); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("isochrone: %d %s", resp.StatusCode, body)
	}
	matrix := `{"sources":[{"lon":37.6,"lat":55.7}],"targets":[{"lon":37.7,"lat":55.8}],"vehicle":{"grossT":20,"heightM":4,"widthM":2.55,"lengthM":10}}`
	if resp, body := f.do(t, http.MethodPost, "/api/v1/routing/matrix", analyst, matrix); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("matrix: %d %s", resp.StatusCode, body)
	}
	// Одна точка — ошибка запроса.
	if resp, _ := f.do(t, http.MethodPost, "/api/v1/routing/route", analyst,
		`{"points":[{"lon":37.6,"lat":55.7}],"vehicle":{"grossT":20,"heightM":4,"widthM":2.55,"lengthM":10}}`); resp.StatusCode != http.StatusBadRequest &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("one point: %d", resp.StatusCode)
	}
}

func TestAPIFacilities(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	osmtest.LoadDistrict(t, f.pool)
	analyst := f.token(t, "fac@example.com", identity.RoleAnalyst)

	resp, body := f.do(t, http.MethodGet, "/api/v1/facilities?limit=3&sort=name", analyst, "")
	var page struct {
		Items []struct {
			ID    string
			Type  string
			Point struct{ Lon, Lat float64 }
		}
		Total int
		Next  string
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Items) != 3 || page.Total < 3 || page.Next == "" {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, http.MethodGet, "/api/v1/facilities?limit=3&sort=name&cursor="+page.Next, analyst, "")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), page.Items[0].ID) {
		t.Errorf("next page: %d %s", resp.StatusCode, body)
	}
	// Фильтры параметрами: тип (несколько значений), рамка.
	it := page.Items[0]
	q := fmt.Sprintf("/api/v1/facilities?type=%s&type=terminal&bbox=%f,%f,%f,%f", it.Type, it.Point.Lon-0.01, it.Point.Lat-0.01,
		it.Point.Lon+0.01, it.Point.Lat+0.01)
	if resp, body := f.do(t, http.MethodGet, q, analyst, ""); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), it.ID) {
		t.Errorf("filtered: %d %s", resp.StatusCode, body)
	}
	if resp, body := f.do(t, http.MethodGet, "/api/v1/facilities/facets?ring=outside", analyst, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), `"types":[{`) {
		t.Errorf("facets: %d %s", resp.StatusCode, body)
	}
	area := fmt.Sprintf(`{"polygon":{"type":"Polygon","coordinates":[[[%[1]f,%[2]f],[%[3]f,%[2]f],[%[3]f,%[4]f],[%[1]f,%[4]f],[%[1]f,%[2]f]]]}}`,
		it.Point.Lon-0.01, it.Point.Lat-0.01, it.Point.Lon+0.01, it.Point.Lat+0.01)
	if resp, raw := f.do(t, http.MethodPost, "/api/v1/facilities/area", analyst, area); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), it.ID) {
		t.Errorf("area: %d %s", resp.StatusCode, raw)
	}
	resp, body = f.do(t, http.MethodGet, "/api/v1/facilities/"+it.ID, analyst, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"confidenceReasons":[{`) {
		t.Errorf("card: %d %s", resp.StatusCode, body)
	}
	if resp, body := f.do(t, http.MethodGet, "/api/v1/facilities/compare?id="+it.ID+"&id="+page.Items[1].ID, analyst, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("compare: %d %s", resp.StatusCode, body)
	}
	// Ошибки: неизвестный объект, сортировка, одно значение для сравнения.
	for path, want := range map[string]int{
		"/api/v1/facilities/" + uuid.NewString():            http.StatusNotFound,
		"/api/v1/facilities?sort=price":                     http.StatusBadRequest,
		"/api/v1/facilities/compare?id=" + uuid.NewString(): http.StatusBadRequest,
	} {
		if resp, body := f.do(t, http.MethodGet, path, analyst, ""); resp.StatusCode != want {
			t.Errorf("%s: %d %s", path, resp.StatusCode, body)
		}
	}
}

func TestAPIExports(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	analyst := f.token(t, "exp@example.com", identity.RoleAnalyst)
	viewer := f.token(t, "view@example.com", identity.RoleViewer)

	body := `{"kind":"facilities","format":"csv","filter":{"type":["warehouse"],"ring":["mkad"]},"sort":"-area"}`
	resp, raw := f.do(t, http.MethodPost, "/api/v1/exports", analyst, body)
	var e struct {
		ID     string
		Status string
	}
	if resp.StatusCode != http.StatusAccepted || json.Unmarshal(raw, &e) != nil || e.Status != "queued" {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	// Файл до формирования — 409; список и состояние — только свои.
	if resp, raw := f.do(t, http.MethodGet, "/api/v1/exports/"+e.ID+"/file", analyst, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("file before run: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := f.do(t, http.MethodGet, "/api/v1/exports", analyst, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), e.ID) {
		t.Errorf("list: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/exports/"+e.ID, viewer, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer: %d", resp.StatusCode)
	}
	var params string
	if err := f.pool.QueryRow(t.Context(), `SELECT params::text FROM report.export WHERE id = $1`, e.ID).Scan(&params); err != nil ||
		!strings.Contains(params, `"warehouse"`) || !strings.Contains(params, `"-area"`) {
		t.Errorf("params %s, %v", params, err)
	}
	var audited int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM core.audit_log WHERE action = 'export.created'`).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audit: %d, %v", audited, err)
	}
}

func TestAPICuration(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	osmtest.LoadDistrict(t, f.pool)
	analyst := f.token(t, "cur-an@example.com", identity.RoleAnalyst)
	steward := f.token(t, "cur-st@example.com", identity.RoleDataSteward)
	var id string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM infra.facility WHERE status = 'active' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/facilities/" + id

	// Аналитик сообщает о неточности, но не правит.
	resp, raw := f.do(t, http.MethodPost, base+"/reports", analyst, `{"message":"Название устарело"}`)
	var rep struct{ ID string }
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(raw, &rep) != nil {
		t.Fatalf("report: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.do(t, http.MethodPost, base+"/curations", analyst, `{"field":"name","value":"x","reason":"y"}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("analyst curate: %d", resp.StatusCode)
	}

	// Редактор: очередь, правка, пометка в карточке, история, закрытие сообщения.
	if resp, raw := f.do(t, http.MethodGet, "/api/v1/facility-reports", steward, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), rep.ID) {
		t.Errorf("queue: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := f.do(t, http.MethodPost, base+"/curations", steward, `{"field":"type_code","value":"factory","reason":"ошибка"}`); resp.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(string(raw), "неизвестен") {
		t.Errorf("bad type: %d %s", resp.StatusCode, raw)
	}
	resp, raw = f.do(t, http.MethodPost, base+"/curations", steward, `{"field":"name","value":"Склад ООО «Север»","reason":"по сообщению аналитика"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("curate: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := f.do(t, http.MethodGet, base, analyst, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), `"curated":["name"]`) || !strings.Contains(string(raw), "Склад ООО «Север»") {
		t.Errorf("card: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := f.do(t, http.MethodGet, base+"/history", analyst, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), "по сообщению аналитика") || !strings.Contains(string(raw), "Название устарело") {
		t.Errorf("history: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := f.do(t, http.MethodPatch, "/api/v1/facility-reports/"+rep.ID, steward, `{"status":"resolved","resolution":"название исправлено"}`); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), `"status":"resolved"`) {
		t.Errorf("resolve: %d %s", resp.StatusCode, raw)
	}
	var audited int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM core.audit_log WHERE action IN ('facility.curated', 'facility.report_resolved')`).Scan(&audited); err != nil || audited != 2 {
		t.Errorf("audit: %d, %v", audited, err)
	}
}

func TestAPIViews(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	analyst := f.token(t, "view-a@example.com", identity.RoleAnalyst)
	other := f.token(t, "view-b@example.com", identity.RoleAnalyst)
	viewer := f.token(t, "view-c@example.com", identity.RoleViewer)
	if resp, raw := f.do(t, http.MethodPost, "/api/v1/views", analyst,
		`{"page":"map","name":"Склады ЮВАО","params":{"l":"facilities","z":12},"role":"analyst"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := f.do(t, http.MethodGet, "/api/v1/views?page=map", other, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(string(raw), `"name":"Склады ЮВАО"`) || !strings.Contains(string(raw), `"z":12`) {
		t.Errorf("shared: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.do(t, http.MethodPost, "/api/v1/views", viewer, `{"page":"map","name":"x","params":{}}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer create: %d", resp.StatusCode)
	}
	if resp, _ := f.do(t, http.MethodPost, "/api/v1/views", analyst, `{"page":"map","name":"x","params":{},"role":"admin"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("foreign role: %d", resp.StatusCode)
	}
}

func TestAPITerritories(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	osmtest.LoadDistrict(t, f.pool)
	analyst := f.token(t, "terr@example.com", identity.RoleAnalyst)

	if resp, _ := f.do(t, http.MethodGet, "/api/v1/territories", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("без входа: %d", resp.StatusCode)
	}
	resp, body := f.do(t, http.MethodGet, "/api/v1/territories?kind=district", analyst, "")
	var list []struct {
		ID         string
		Kind       string
		Facilities int
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list) == 0 {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	id := ""
	for _, r := range list {
		if r.Kind != "district" {
			t.Fatalf("kind %q", r.Kind)
		}
		if r.Facilities > 0 {
			id = r.ID
		}
	}
	resp, body = f.do(t, http.MethodGet, "/api/v1/territories/"+id, analyst, "")
	var p struct {
		Geometry struct{ Type string }
		Bbox     []float64
		Summary  struct{ Facilities int }
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &p) != nil || p.Summary.Facilities == 0 || len(p.Bbox) != 4 ||
		p.Geometry.Type == "" {
		t.Errorf("profile: %d %s", resp.StatusCode, body)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/territories/"+uuid.NewString(), analyst, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("нет территории: %d", resp.StatusCode)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/territories?kind=region", analyst, ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("неверный уровень: %d", resp.StatusCode)
	}
}

// Контракт рабочего пространства: права ролей на операции реестра, выгрузок, видов, уточнения и
// территорий; разбор и проверка фильтров.
func TestAPIWorkspaceContract(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	osmtest.LoadDistrict(t, f.pool)
	viewer := f.token(t, "wv@example.com", identity.RoleViewer)
	analyst := f.token(t, "wa@example.com", identity.RoleAnalyst)
	steward := f.token(t, "ws@example.com", identity.RoleDataSteward)
	var id, district string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text, district_id::text FROM infra.facility
		WHERE status = 'active' AND district_id IS NOT NULL ORDER BY building_area_m2 DESC NULLS LAST LIMIT 1`).Scan(&id, &district); err != nil {
		t.Fatal(err)
	}
	fac := "/api/v1/facilities/" + id
	poly := `{"polygon":{"type":"Polygon","coordinates":[[[37.6,55.7],[37.8,55.7],[37.8,55.8],[37.6,55.8],[37.6,55.7]]]}}`

	cases := []struct {
		name, method, path, token, body string
		want                            int
	}{
		// Права: просмотр — всем ролям, выгрузки и виды — с аналитика, уточнение — редактору.
		{"viewer lists registry", http.MethodGet, "/api/v1/facilities?limit=1", viewer, "", http.StatusOK},
		{"viewer reads facets", http.MethodGet, "/api/v1/facilities/facets", viewer, "", http.StatusOK},
		{"viewer reads points", http.MethodGet, "/api/v1/facilities/points", viewer, "", http.StatusOK},
		{"viewer reads card", http.MethodGet, fac, viewer, "", http.StatusOK},
		{"viewer reads history", http.MethodGet, fac + "/history", viewer, "", http.StatusOK},
		{"viewer summarizes area", http.MethodPost, "/api/v1/facilities/area", viewer, poly, http.StatusOK},
		{"viewer lists territories", http.MethodGet, "/api/v1/territories", viewer, "", http.StatusOK},
		{"viewer reads territory", http.MethodGet, "/api/v1/territories/" + district, viewer, "", http.StatusOK},
		{"viewer lists views", http.MethodGet, "/api/v1/views?page=map", viewer, "", http.StatusOK},
		{"viewer cannot save view", http.MethodPost, "/api/v1/views", viewer, `{"page":"map","name":"x","params":{}}`, http.StatusForbidden},
		{"viewer cannot export", http.MethodPost, "/api/v1/exports", viewer, `{"kind":"facilities","format":"csv","filter":{}}`, http.StatusForbidden},
		{"viewer cannot list exports", http.MethodGet, "/api/v1/exports", viewer, "", http.StatusForbidden},
		{"viewer reports issue", http.MethodPost, fac + "/reports", viewer, `{"message":"Объект закрыт"}`, http.StatusCreated},
		{"analyst saves view", http.MethodPost, "/api/v1/views", analyst, `{"page":"facilities","name":"Склады","params":{"type":"warehouse"}}`, http.StatusCreated},
		{"analyst lists exports", http.MethodGet, "/api/v1/exports", analyst, "", http.StatusOK},
		{"analyst cannot curate", http.MethodPost, fac + "/curations", analyst, `{"field":"name","value":"x","reason":"y"}`, http.StatusForbidden},
		{"analyst cannot read report queue", http.MethodGet, "/api/v1/facility-reports", analyst, "", http.StatusForbidden},
		{"steward reads report queue", http.MethodGet, "/api/v1/facility-reports", steward, "", http.StatusOK},
		{"anonymous cannot list", http.MethodGet, "/api/v1/facilities", "", "", http.StatusUnauthorized},
		{"anonymous cannot read points", http.MethodGet, "/api/v1/facilities/points", "", "", http.StatusUnauthorized},

		// Фильтры: допустимые сочетания и отказ на неверных значениях.
		{"all filters", http.MethodGet, "/api/v1/facilities?q=склад&type=warehouse&type=terminal&district=" + district +
			"&ring=mkad&ring=ckad&minArea=100&maxArea=1000000&minConfidence=0.4&status=active&bbox=37.5,55.6,37.9,55.9" +
			"&maxMkadMin=60&nearRestrictions=false&hasPermit=false&sort=-area,name", analyst, "", http.StatusOK},
		{"radius filter", http.MethodGet, "/api/v1/facilities?near=37.7,55.73,3000", analyst, "", http.StatusOK},
		{"unknown ring", http.MethodGet, "/api/v1/facilities?ring=moon", analyst, "", http.StatusBadRequest},
		{"confidence over 1", http.MethodGet, "/api/v1/facilities?minConfidence=1.5", analyst, "", http.StatusBadRequest},
		{"bbox of three", http.MethodGet, "/api/v1/facilities?bbox=37.5,55.6,37.9", analyst, "", http.StatusBadRequest},
		{"inverted bbox", http.MethodGet, "/api/v1/facilities?bbox=37.9,55.9,37.5,55.6", analyst, "", http.StatusBadRequest},
		{"unknown sort", http.MethodGet, "/api/v1/facilities?sort=salary", analyst, "", http.StatusBadRequest},
		{"bad cursor", http.MethodGet, "/api/v1/facilities?cursor=%21%21", analyst, "", http.StatusBadRequest},
		{"district not uuid", http.MethodGet, "/api/v1/facilities?district=ЮВАО", analyst, "", http.StatusBadRequest},
		{
			"area is a point", http.MethodPost, "/api/v1/facilities/area", analyst,
			`{"polygon":{"type":"Point","coordinates":[37.6,55.7]}}`, http.StatusBadRequest,
		},
		{"compare one", http.MethodGet, "/api/v1/facilities/compare?ids=" + id, analyst, "", http.StatusBadRequest},
		{"unknown facility", http.MethodGet, "/api/v1/facilities/" + uuid.NewString(), analyst, "", http.StatusNotFound},
		{"unknown territory kind", http.MethodGet, "/api/v1/territories?kind=planet", analyst, "", http.StatusBadRequest},
		{"unknown view page", http.MethodGet, "/api/v1/views?page=moon", analyst, "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := f.do(t, tc.method, tc.path, tc.token, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d; body %s", resp.StatusCode, tc.want, body)
			}
		})
	}

	// Фасет не учитывает собственный фильтр: при отборе по типу видны и другие типы.
	resp, body := f.do(t, http.MethodGet, "/api/v1/facilities/facets?type=warehouse", analyst, "")
	var facets struct{ Types []struct{ Value string } }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &facets) != nil || len(facets.Types) < 2 {
		t.Errorf("facets: %d %s", resp.StatusCode, body)
	}
	// Фильтр района сужает выборку до объектов района.
	_, all := f.do(t, http.MethodGet, "/api/v1/facilities?limit=1", analyst, "")
	_, byDistrict := f.do(t, http.MethodGet, "/api/v1/facilities?limit=1&district="+district, analyst, "")
	var a, d struct{ Total int }
	if json.Unmarshal(all, &a) != nil || json.Unmarshal(byDistrict, &d) != nil || d.Total == 0 || d.Total > a.Total {
		t.Errorf("district filter: %d of %d", d.Total, a.Total)
	}
}

func TestAPISearch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	osmtest.LoadDistrict(t, f.pool)
	analyst := f.token(t, "sa@example.com", identity.RoleAnalyst)
	other := f.token(t, "sb@example.com", identity.RoleAnalyst)
	steward := f.token(t, "ss@example.com", identity.RoleDataSteward)
	if resp, raw := f.do(t, http.MethodPost, "/api/v1/views", analyst, `{"page":"map","name":"Склады у реки","params":{}}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("view: %d %s", resp.StatusCode, raw)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO ingest.source (code, title, publisher, url, license, attribution, domains)
		VALUES ('mos_test', 'Склады и терминалы', 'Портал', 'https://data.mos.ru', 'CC BY 4.0', 'data.mos.ru', '{facility}')
		ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := search.New(f.pool, facility.New(f.pool)).Reindex(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	var district string
	if err := f.pool.QueryRow(t.Context(), `SELECT name FROM territory.admin_area WHERE kind = 'district'
		AND id IN (SELECT district_id FROM infra.facility) LIMIT 1`).Scan(&district); err != nil {
		t.Fatal(err)
	}

	type result struct {
		Parsed struct {
			Facility bool
			Types    []string
			Chips    []struct{ Field, Label string }
		}
		Hits       []struct{ Kind, Ref, Title string }
		Facilities *struct{ Total int }
	}
	get := func(token, path string) (int, result, string) {
		resp, raw := f.do(t, http.MethodGet, path, token, "")
		var r result
		_ = json.Unmarshal(raw, &r)
		return resp.StatusCode, r, string(raw)
	}

	// Запрос с фильтрами: тип и район — объекты реестра по фильтрам.
	code, r, raw := get(analyst, "/api/v1/search?q="+url.QueryEscape("склады "+district))
	if code != http.StatusOK || !r.Parsed.Facility || len(r.Parsed.Types) != 1 || r.Facilities == nil || r.Facilities.Total == 0 ||
		len(r.Parsed.Chips) != 2 {
		t.Errorf("фильтры: %d %s", code, raw)
	}
	// Территория без фильтров — документ.
	code, r, raw = get(analyst, "/api/v1/search?q="+url.QueryEscape(district))
	if code != http.StatusOK || r.Parsed.Facility || len(r.Hits) == 0 || r.Hits[0].Kind != "territory" {
		t.Errorf("территория: %d %s", code, raw)
	}
	// Сохранённый вид виден владельцу, но не другому аналитику; наборы данных — управляющим данными.
	hasKind := func(r result, kind string) bool {
		for _, h := range r.Hits {
			if h.Kind == kind {
				return true
			}
		}
		return false
	}
	if _, r, raw = get(analyst, "/api/v1/search?q="+url.QueryEscape("склады у реки")); !hasKind(r, "saved_view") {
		t.Errorf("свой вид: %s", raw)
	}
	if _, r, raw = get(other, "/api/v1/search?q="+url.QueryEscape("склады у реки")); hasKind(r, "saved_view") {
		t.Errorf("чужой вид: %s", raw)
	}
	if _, r, _ = get(analyst, "/api/v1/search?q="+url.QueryEscape("терминалы портал")); hasKind(r, "dataset") {
		t.Errorf("набор данных у аналитика")
	}
	if _, r, raw = get(steward, "/api/v1/search?q="+url.QueryEscape("склады и терминалы портал")); !hasKind(r, "dataset") {
		t.Errorf("набор данных у редактора: %s", raw)
	}

	// Подсказки, разбор, геокодер.
	resp, body := f.do(t, http.MethodGet, "/api/v1/search/suggest?q="+url.QueryEscape(district[:len(district)/2]), analyst, "")
	var hits []struct{ Kind string }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &hits) != nil || len(hits) == 0 {
		t.Errorf("подсказки: %d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, http.MethodGet, "/api/v1/search/parse?q="+url.QueryEscape("РЦ больше 2 га у МКАД"), analyst, "")
	var parsed struct {
		Types      []string
		MinArea    float64
		MaxMkadMin float64
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &parsed) != nil || parsed.MinArea != 20000 ||
		parsed.MaxMkadMin != 10 || len(parsed.Types) != 1 {
		t.Errorf("разбор: %d %s", resp.StatusCode, body)
	}
	var lon, lat float64
	if err := f.pool.QueryRow(t.Context(), `SELECT ST_X(point), ST_Y(point) FROM search.document WHERE kind = 'territory' AND subtitle LIKE 'Район%' LIMIT 1`).
		Scan(&lon, &lat); err != nil {
		t.Fatal(err)
	}
	resp, body = f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/geocode/reverse?lon=%f&lat=%f", lon, lat), analyst, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"territory"`) {
		t.Errorf("обратный геокодер: %d %s", resp.StatusCode, body)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/search?q=", analyst, ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("пустой запрос: %d", resp.StatusCode)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/search?q=x", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("без входа: %d", resp.StatusCode)
	}
}

func TestAPIMetrics(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	osmtest.LoadDistrict(t, f.pool)
	viewer := f.token(t, "mv@example.com", identity.RoleViewer)
	if _, err := analytics.New(f.pool).Recompute(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}

	resp, body := f.do(t, http.MethodGet, "/api/v1/metrics", viewer, "")
	var defs []struct {
		Code, Method string
		Rows         int
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &defs) != nil || len(defs) != len(analytics.Catalog()) ||
		defs[0].Method == "" {
		t.Fatalf("каталог: %d %s", resp.StatusCode, body)
	}

	resp, body = f.do(t, http.MethodGet, "/api/v1/metrics/territory.facilities?level=okrug", viewer, "")
	var vals struct {
		Metric struct{ Code string }
		Values []struct {
			Entity, Label string
			Value         float64
			Rank, Of      int
		}
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &vals) != nil || len(vals.Values) == 0 ||
		vals.Values[0].Label == "" || vals.Values[0].Rank != 1 || vals.Values[0].Of != len(vals.Values) {
		t.Fatalf("рейтинг округов: %d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, http.MethodGet, "/api/v1/metrics/entity?level=okrug&entity="+vals.Values[0].Entity, viewer, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"territory.facilities"`) || !strings.Contains(string(body), `"rank":1`) {
		t.Errorf("показатели округа: %d %s", resp.StatusCode, body)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/metrics/territory.facilities?level=segment", viewer, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("уровень не показателя: %d", resp.StatusCode)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/metrics/nope.metric", viewer, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("нет показателя: %d", resp.StatusCode)
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/metrics", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("без входа: %d", resp.StatusCode)
	}
}

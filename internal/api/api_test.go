package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	spec "freightflow/api"
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
	"freightflow/internal/tiles"
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
		Reports: report.New(pool, inserter, blobs, facilities), Sessions: sessions, Mailer: mailer,
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
	if resp, body := f.do(t, http.MethodGet, "/api/v1/routing/places?q=%D0%93%D0%B0%D0%B7%D0%B3%D0%BE%D0%BB%D1%8C%D0%B4%D0%B5%D1%80%D0%BD%D0%B0%D1%8F", analyst, ""); resp.StatusCode != http.StatusOK ||
		string(body) != "[]\n" && string(body) != "[]" {
		t.Errorf("places: %d %s", resp.StatusCode, body)
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

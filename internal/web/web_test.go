package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"freightflow/internal/identity"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/secret"
	"freightflow/internal/platform/session"
)

const testPassword = "пароль для тестов 1"

type nopMailer struct{}

func (nopMailer) Send(context.Context, mail.Message) error { return nil }

type fixture struct {
	srv      *httptest.Server
	identity *identity.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.New(t)
	box, err := secret.NewBox(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	ids := identity.NewService(pool, box)
	if err := ids.SyncBuiltinRoles(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions := session.New(pool, false)
	h, err := New(Deps{
		Identity: ids, Sessions: sessions, Mailer: nopMailer{}, BaseURL: "http://test.local",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpx.Chain(h, httpx.WithClient(), sessions.LoadAndSave))
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, identity: ids}
}

func (f *fixture) user(t *testing.T, email, role string) identity.User {
	t.Helper()
	u, err := f.identity.CreateUser(context.Background(), nil, identity.CreateUserInput{
		Email: email, Name: "Тест", Password: testPassword, Roles: []string{role},
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// client — браузер без автоматических переходов по редиректам.
func (f *fixture) client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func get(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return do(t, c, req)
}

func post(t *testing.T, c *http.Client, u string, form url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(t, c, req)
}

func do(t *testing.T, c *http.Client, req *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func expectRedirect(t *testing.T, resp *http.Response, location string) {
	t.Helper()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != location {
		t.Fatalf("Location %q, want %q", got, location)
	}
}

func TestSafeNext(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                     "/",
		"/map?layer=works":     "/map?layer=works",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
		"map":                  "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAppRequiresSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	resp, _ := get(t, f.client(t), f.srv.URL+"/profile?tab=tokens")
	expectRedirect(t, resp, "/login?next=%2Fprofile%3Ftab%3Dtokens")
}

func TestLogin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.user(t, "viewer@example.com", identity.RoleViewer)
	c := f.client(t)

	resp, _ := post(t, c, f.srv.URL+"/login", url.Values{
		"email": {"viewer@example.com"}, "password": {"не тот пароль"}, "next": {"/map"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password: status %d", resp.StatusCode)
	}

	resp, _ = post(t, c, f.srv.URL+"/login", url.Values{
		"email": {"viewer@example.com"}, "password": {testPassword}, "next": {"/map"},
	})
	expectRedirect(t, resp, "/map")

	// Вошедшего пользователя страница входа отправляет дальше.
	resp, _ = get(t, c, f.srv.URL+"/login?next=/profile")
	expectRedirect(t, resp, "/profile")
}

var secretRe = regexp.MustCompile(`<div class="secret-code">([A-Z2-7]+)</div>`)

func TestAdminSetsUpSecondFactor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	admin := f.user(t, "admin@example.com", identity.RoleAdmin)
	c := f.client(t)

	// Администратор без 2FA после пароля попадает на подключение с сохранением адреса возврата.
	resp, _ := post(t, c, f.srv.URL+"/login", url.Values{
		"email": {"admin@example.com"}, "password": {testPassword}, "next": {"/admin/users"},
	})
	expectRedirect(t, resp, "/setup-2fa?next=%2Fadmin%2Fusers")

	resp, body := get(t, c, f.srv.URL+"/setup-2fa?next=%2Fadmin%2Fusers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup page: status %d", resp.StatusCode)
	}
	if !strings.Contains(body, `name="next" value="/admin/users"`) {
		t.Fatal("setup form must carry next")
	}
	m := secretRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("secret not found on setup page")
	}
	code, err := totp.GenerateCode(m[1], time.Now())
	if err != nil {
		t.Fatal(err)
	}

	resp, body = post(t, c, f.srv.URL+"/setup-2fa", url.Values{"code": {code}, "next": {"/admin/users"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `href="/admin/users"`) {
		t.Fatalf("recovery codes page: status %d", resp.StatusCode)
	}

	// Повторное открытие страницы не меняет секрет и не отключает защиту.
	resp, _ = get(t, c, f.srv.URL+"/setup-2fa?next=/profile")
	expectRedirect(t, resp, "/profile")
	res, err := f.identity.Authenticate(context.Background(), admin.Email, testPassword, identity.RequestMeta{})
	if err != nil || !res.NeedsSecondFactor {
		t.Fatalf("2FA must stay enabled: %+v %v", res, err)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.user(t, "analyst@example.com", identity.RoleAnalyst)
	c := f.client(t)

	resp, _ := post(t, c, f.srv.URL+"/login", url.Values{
		"email": {"analyst@example.com"}, "password": {testPassword},
	})
	expectRedirect(t, resp, "/")

	resp, _ = post(t, c, f.srv.URL+"/logout", nil)
	expectRedirect(t, resp, "/login")

	resp, _ = get(t, c, f.srv.URL+"/profile")
	expectRedirect(t, resp, "/login?next=%2Fprofile")
}

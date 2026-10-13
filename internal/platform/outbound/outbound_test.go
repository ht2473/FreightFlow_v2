package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, srv *httptest.Server, allowPrivate bool) (*Client, *[]time.Duration) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	c, err := New(t.Context(), Config{Hosts: []string{u.Hostname()}, Direct: true, AllowPrivate: allowPrivate, Retries: 3})
	if err != nil {
		t.Fatal(err)
	}
	var pauses []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		return nil
	}
	return c, &pauses
}

func TestRetriesTransientFailures(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Header().Set("ETag", `"v1"`)
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()
	c, pauses := newTestClient(t, srv, true)

	resp, err := c.Get(context.Background(), srv.URL+"/data?api_key=secret", Validators{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok" || resp.Validators.ETag != `"v1"` || calls.Load() != 3 {
		t.Fatalf("body=%q etag=%q calls=%d", body, resp.Validators.ETag, calls.Load())
	}
	if len(*pauses) != 2 || (*pauses)[0] != 7*time.Second || (*pauses)[1] != 2*time.Second {
		t.Fatalf("pauses: %v", *pauses)
	}
}

func TestClientErrorIsNotRetried(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, true)

	_, err := c.Get(context.Background(), srv.URL, Validators{})
	if StatusCode(err) != http.StatusNotFound || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestDefaultHeaders(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Как transport.mos.ru: без Accept — отказ.
		if r.Header.Get("Accept") == "" || r.Header.Get("User-Agent") != "FreightFlow" {
			w.WriteHeader(477)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, true)

	resp, err := c.Get(context.Background(), srv.URL, Validators{})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestCookiesKeptWhenEnabled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/"})
			return
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "s1" {
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	for _, cookies := range []bool{true, false} {
		c, err := New(t.Context(), Config{Hosts: []string{u.Hostname()}, Direct: true, AllowPrivate: true, Retries: 1, Cookies: cookies})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Get(context.Background(), srv.URL+"/page", Validators{})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		resp, err = c.Post(context.Background(), srv.URL+"/data", "application/x-www-form-urlencoded", []byte("a=1"))
		if cookies != (err == nil) {
			t.Fatalf("cookies=%v: err=%v", cookies, err)
		}
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

func TestConditionalRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.WriteString(w, "fresh")
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, true)

	if _, err := c.Get(context.Background(), srv.URL, Validators{ETag: `"v1"`}); !errors.Is(err, ErrNotModified) {
		t.Fatalf("want ErrNotModified, got %v", err)
	}
}

func TestGetFromResumesWithRange(t *testing.T) {
	t.Parallel()
	const body = "0123456789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f.bin", time.Time{}, strings.NewReader(body))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, true)

	resp, err := c.GetFrom(t.Context(), srv.URL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent || string(got) != body[4:] {
		t.Fatalf("status %d, body %q", resp.StatusCode, got)
	}
}

func TestBlocksPrivateNetworkAndForeignHosts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.org/", http.StatusFound)
	}))
	defer srv.Close()

	strict, _ := newTestClient(t, srv, false)
	if _, err := strict.Get(context.Background(), srv.URL, Validators{}); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("private address: %v", err)
	}

	lax, _ := newTestClient(t, srv, true)
	if _, err := lax.Get(context.Background(), srv.URL, Validators{}); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("redirect to foreign host: %v", err)
	}
	if _, err := lax.Get(context.Background(), "https://example.org/", Validators{}); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("foreign host: %v", err)
	}
}

func TestHostAllowed(t *testing.T) {
	t.Parallel()
	hosts := []string{"apidata.mos.ru", "*.openstreetmap.fr"}
	cases := map[string]bool{
		"apidata.mos.ru":            true,
		"APIDATA.MOS.RU.":           true,
		"evil-apidata.mos.ru":       false,
		"download.openstreetmap.fr": true,
		"openstreetmap.fr":          false,
		"openstreetmap.fr.evil.io":  false,
	}
	for host, want := range cases {
		if got := hostAllowed(hosts, host); got != want {
			t.Errorf("%s: got %v", host, got)
		}
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	if got := redact("https://user:pw@apidata.mos.ru/v1/datasets?api_key=secret"); got != "https://apidata.mos.ru/v1/datasets" {
		t.Fatalf("redact: %s", got)
	}
}

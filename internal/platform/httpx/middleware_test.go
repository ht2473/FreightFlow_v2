package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"freightflow/internal/platform/problem"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestRequestIDGeneratedAndPropagated(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}), WithRequestID())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || rec.Header().Get("X-Request-ID") != seen {
		t.Fatalf("request id not propagated: ctx=%q header=%q", seen, rec.Header().Get("X-Request-ID"))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-id")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "client-id" {
		t.Fatalf("client request id must be kept, got %q", seen)
	}
}

func TestRecoverRespondsWithProblem(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), Recover(discardLogger()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content type = %q", ct)
	}
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "internal" || p.Instance != "/x" {
		t.Fatalf("unexpected problem: %+v", p)
	}
}

func TestSecurityHeadersSetNonce(t *testing.T) {
	var nonce string
	h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonce = CSPNonce(r.Context())
	}), SecurityHeaders(true))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if nonce == "" || !strings.Contains(csp, "'nonce-"+nonce+"'") {
		t.Fatalf("nonce %q not in CSP %q", nonce, csp)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS must be set when strictTransport is true")
	}
}

func TestCrossOriginRejectsCrossSiteMutation(t *testing.T) {
	mw, err := CrossOrigin()
	if err != nil {
		t.Fatal(err)
	}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), mw)

	cross := httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, cross)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST: status = %d, want 403", rec.Code)
	}

	same := httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	same.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, same)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("same-origin POST: status = %d, want 204", rec.Code)
	}
}

func TestAccessLogRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), AccessLog(log))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/tea", nil))
	if !strings.Contains(buf.String(), "status=418") || !strings.Contains(buf.String(), "path=/tea") {
		t.Fatalf("unexpected log: %s", buf.String())
	}
}

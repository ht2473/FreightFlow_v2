package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"freightflow/internal/identity"
	"freightflow/internal/platform/problem"
	"freightflow/internal/platform/ratelimit"
)

func TestRoutingLimitPerUser(t *testing.T) {
	t.Parallel()
	s := &Server{routeLimit: ratelimit.New(time.Hour, 3)}
	user := func(id uuid.UUID) context.Context {
		return context.WithValue(t.Context(), principalKey{}, identity.Principal{UserID: id})
	}
	a, b := user(uuid.New()), user(uuid.New())
	if err := s.spend(a, 2); err != nil {
		t.Fatal(err)
	}
	var p *problem.Problem
	if err := s.spend(a, 2); !errors.As(err, &p) || p.Status != http.StatusTooManyRequests {
		t.Errorf("over limit: %v", err)
	}
	// Лимит — у каждого пользователя свой.
	if err := s.spend(b, 3); err != nil {
		t.Errorf("other user: %v", err)
	}
}

func TestAPITokenRateLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tok := f.token(t, "limit@example.com", identity.RoleViewer)
	other := f.token(t, "limit2@example.com", identity.RoleViewer)
	limited := false
	for i := 0; i < tokenBurst+20 && !limited; i++ {
		resp, _ := f.do(t, http.MethodGet, "/api/v1/me", tok, "")
		limited = resp.StatusCode == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("token was not limited")
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/me", other, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("other token: %d", resp.StatusCode)
	}
}

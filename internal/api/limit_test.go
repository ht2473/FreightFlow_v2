package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
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
	// Запросы идут параллельно: последовательно на медленной машине ведро успевает пополниться.
	var limited atomic.Bool
	var wg sync.WaitGroup
	slots := make(chan struct{}, 32)
	for range 2 * tokenBurst {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			if code, _ := f.status(http.MethodGet, "/api/v1/me", tok, ""); code == http.StatusTooManyRequests {
				limited.Store(true)
			}
		}()
	}
	wg.Wait()
	if !limited.Load() {
		t.Fatal("token was not limited")
	}
	if resp, _ := f.do(t, http.MethodGet, "/api/v1/me", other, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("other token: %d", resp.StatusCode)
	}
}

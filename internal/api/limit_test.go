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

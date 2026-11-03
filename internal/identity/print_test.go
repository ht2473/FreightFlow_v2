package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPrintToken(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	admin := createUser(t, s, "print-admin@example.com", RoleAdmin)

	tok := s.IssuePrintToken(admin.ID, 10*time.Minute)
	p, err := s.AuthenticateAPIToken(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadOnly || p.UserID != admin.ID || !p.Has(PermMapView) || p.Has(PermUsersManage) || p.TokenID != nil {
		t.Fatalf("print principal: %+v", p)
	}

	// Подделка и чужая подпись.
	body, sig, _ := strings.Cut(strings.TrimPrefix(tok, printPrefix), ".")
	for _, bad := range []string{tok + "x", printPrefix + body, printPrefix + "AAAA." + sig, printPrefix + body + ".AAAA"} {
		if _, err := s.AuthenticateAPIToken(ctx, bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}

	// Срок вышел.
	s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, err := s.AuthenticateAPIToken(ctx, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: %v", err)
	}
	s.now = time.Now

	// Заблокированный пользователь.
	if _, err := s.pool.Exec(ctx, `UPDATE core.app_user SET status = 'blocked' WHERE id = $1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAPIToken(ctx, tok); !errors.Is(err, ErrAccountBlocked) {
		t.Fatalf("blocked user: %v", err)
	}
}

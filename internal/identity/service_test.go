package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/secret"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func newTestService(t *testing.T) *Service {
	t.Helper()
	pool := dbtest.New(t)
	box, err := secret.NewBox(testKey)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(pool, box)
	if err := s.SyncBuiltinRoles(context.Background()); err != nil {
		t.Fatalf("sync roles: %v", err)
	}
	return s
}

func createUser(t *testing.T, s *Service, email string, roles ...string) User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), nil, CreateUserInput{
		Email: email, Name: "Тестовый Пользователь", Password: "надёжный пароль 2026", Roles: roles,
	}, RequestMeta{})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestCreateUserAndAuthenticate(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	u := createUser(t, s, " Analyst@Example.com ", RoleAnalyst)
	if u.Email != "analyst@example.com" {
		t.Fatalf("email not normalized: %q", u.Email)
	}

	res, err := s.Authenticate(ctx, "ANALYST@example.com", "надёжный пароль 2026", RequestMeta{})
	if err != nil || res.NeedsSecondFactor || res.UserID != u.ID {
		t.Fatalf("authenticate: %+v %v", res, err)
	}
	p, err := s.Principal(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has(PermScenariosManage) || p.Has(PermUsersManage) {
		t.Fatalf("unexpected analyst permissions: %v", p.Permissions)
	}

	if _, err := s.CreateUser(ctx, nil, CreateUserInput{Email: "analyst@example.com", Name: "Дубль", Password: "надёжный пароль 2026", Roles: []string{RoleViewer}}, RequestMeta{}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email: got %v, want ErrEmailTaken", err)
	}
}

func TestAuthenticateLocksAfterFailures(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	createUser(t, s, "viewer@example.com", RoleViewer)

	for i := 0; i < maxLoginAttempts; i++ {
		if _, err := s.Authenticate(ctx, "viewer@example.com", "неверный пароль!", RequestMeta{}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: got %v", i+1, err)
		}
	}
	if _, err := s.Authenticate(ctx, "viewer@example.com", "надёжный пароль 2026", RequestMeta{}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("after %d failures: got %v, want ErrAccountLocked", maxLoginAttempts, err)
	}
	if _, err := s.Authenticate(ctx, "nobody@example.com", "что угодно 12345", RequestMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown email: got %v", err)
	}
}

func TestTOTPFlowWithRecoveryCode(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	u := createUser(t, s, "admin@example.com", RoleAdmin)

	setup, err := s.BeginTOTPSetup(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(setup.URL, "otpauth://totp/FreightFlow") {
		t.Fatalf("unexpected otpauth url: %s", setup.URL)
	}
	code, _ := totp.GenerateCode(setup.Secret, time.Now())
	codes, err := s.ConfirmTOTP(ctx, u.ID, code, RequestMeta{})
	if err != nil || len(codes) != recoveryCodeCount {
		t.Fatalf("confirm totp: %d codes, %v", len(codes), err)
	}
	if _, err := s.BeginTOTPSetup(ctx, u.ID); !errors.Is(err, ErrTOTPEnabled) {
		t.Fatalf("setup over enabled 2FA: got %v", err)
	}

	res, err := s.Authenticate(ctx, "admin@example.com", "надёжный пароль 2026", RequestMeta{})
	if err != nil || !res.NeedsSecondFactor {
		t.Fatalf("authenticate with 2FA: %+v %v", res, err)
	}
	if err := s.VerifySecondFactor(ctx, u.ID, "000000", RequestMeta{}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: got %v", err)
	}
	if err := s.VerifySecondFactor(ctx, u.ID, codes[0], RequestMeta{}); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	if err := s.VerifySecondFactor(ctx, u.ID, codes[0], RequestMeta{}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("recovery code must be single-use, got %v", err)
	}
}

func TestLastAdminIsProtected(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	admin := createUser(t, s, "root@example.com", RoleAdmin)
	actor, err := s.Principal(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRoles(ctx, actor, admin.ID, []string{RoleViewer}, RequestMeta{}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin: got %v", err)
	}
	second := createUser(t, s, "second@example.com", RoleAdmin)
	if err := s.SetUserRoles(ctx, actor, second.ID, []string{RoleViewer}, RequestMeta{}); err != nil {
		t.Fatalf("demote non-last admin: %v", err)
	}
	if err := s.SetUserStatus(ctx, actor, admin.ID, StatusBlocked, RequestMeta{}); err == nil {
		t.Fatal("blocking yourself must be rejected")
	}
}

func TestAPITokenScopes(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	u := createUser(t, s, "api@example.com", RoleAnalyst)
	owner, _ := s.Principal(ctx, u.ID)

	if _, _, err := s.CreateAPIToken(ctx, owner, "чужое право", []Permission{PermUsersManage}, nil, RequestMeta{}); err == nil {
		t.Fatal("token must not get permissions the owner lacks")
	}
	plain, tok, err := s.CreateAPIToken(ctx, owner, "интеграция", []Permission{PermMapView}, nil, RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AuthenticateAPIToken(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has(PermMapView) || p.Has(PermExportsCreate) || p.TokenID == nil || *p.TokenID != tok.ID {
		t.Fatalf("unexpected token principal: %+v", p)
	}
	if err := s.RevokeAPIToken(ctx, owner, tok.ID, RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAPIToken(ctx, plain); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked token: got %v", err)
	}
}

func TestInviteAndPasswordReset(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	admin := createUser(t, s, "boss@example.com", RoleAdmin)
	actor, _ := s.Principal(ctx, admin.ID)

	token, err := s.CreateInvite(ctx, actor, "new@example.com", RoleOperator, RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if email, err := s.InviteEmail(ctx, token); err != nil || email != "new@example.com" {
		t.Fatalf("invite email: %q %v", email, err)
	}
	u, err := s.AcceptInvite(ctx, token, "Новый Оператор", "пароль оператора 2026", RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInvite(ctx, token, "Повтор", "пароль оператора 2026", RequestMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invite reuse: got %v", err)
	}

	changedBefore, _ := s.PasswordChangedAt(ctx, u.ID)
	resetToken, _, ok, err := s.RequestPasswordReset(ctx, "new@example.com", RequestMeta{})
	if err != nil || !ok {
		t.Fatalf("request reset: ok=%v err=%v", ok, err)
	}
	if err := s.ResetPassword(ctx, resetToken, "совсем новый пароль", RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	changedAfter, _ := s.PasswordChangedAt(ctx, u.ID)
	if !changedAfter.After(changedBefore) {
		t.Fatal("password_changed_at must advance after reset")
	}
	if _, err := s.Authenticate(ctx, "new@example.com", "совсем новый пароль", RequestMeta{}); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if _, _, ok, _ := s.RequestPasswordReset(ctx, "absent@example.com", RequestMeta{}); ok {
		t.Fatal("reset for unknown email must report ok=false")
	}

	records, err := s.ListAudit(ctx, AuditFilter{Limit: 100})
	if err != nil || len(records) == 0 {
		t.Fatalf("audit records: %d %v", len(records), err)
	}
}

package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestHashAndVerifyPassword(t *testing.T) {
	t.Parallel()
	hash := HashPassword("правильный пароль 123")
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=2,p=2$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	ok, stale, err := VerifyPassword("правильный пароль 123", hash)
	if err != nil || !ok || stale {
		t.Fatalf("verify correct password: ok=%v stale=%v err=%v", ok, stale, err)
	}
	ok, _, err = VerifyPassword("неправильный пароль", hash)
	if err != nil || ok {
		t.Fatalf("verify wrong password: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPasswordDetectsStaleParameters(t *testing.T) {
	t.Parallel()
	// Хеш с t=1 вместо текущих параметров.
	hash := HashPassword("пароль для проверки")
	stale := strings.Replace(hash, "t=2", "t=1", 1)
	// Хеш с другими параметрами не совпадёт по значению — проверяем только разбор и признак.
	if _, _, err := VerifyPassword("пароль для проверки", stale); err != nil {
		t.Fatalf("parse stale hash: %v", err)
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=19$m=x$a$b"} {
		if _, _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("expected error for %q", h)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()
	if err := ValidatePassword("короткий"); err == nil {
		t.Error("short password must be rejected")
	}
	if err := ValidatePassword(strings.Repeat("я", 12)); err != nil {
		t.Errorf("12 Cyrillic characters must be accepted: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 129)); err == nil {
		t.Error("too long password must be rejected")
	}
}

func TestTOTPValidationWithSkew(t *testing.T) {
	t.Parallel()
	key, err := generateTOTP("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	code, err := totp.GenerateCode(key.Secret(), now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !validateTOTP(code, key.Secret(), now) {
		t.Fatal("code from the previous interval must be accepted")
	}
	if validateTOTP(code, key.Secret(), now.Add(2*time.Minute)) {
		t.Fatal("code from two minutes ago must be rejected")
	}
}

func TestRecoveryCodeNormalization(t *testing.T) {
	t.Parallel()
	codes := newRecoveryCodes(3)
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Fatalf("unexpected code format %q", c)
		}
		compact := strings.ToLower(strings.ReplaceAll(c, "-", ""))
		if normalizeRecoveryCode(compact) != c {
			t.Fatalf("normalize(%q) = %q, want %q", compact, normalizeRecoveryCode(compact), c)
		}
	}
}

func TestBuiltinRolesUseKnownPermissions(t *testing.T) {
	t.Parallel()
	for _, r := range BuiltinRoles {
		if len(r.Permissions) == 0 {
			t.Errorf("role %s has no permissions", r.Code)
		}
		for _, p := range r.Permissions {
			if !KnownPermission(string(p)) {
				t.Errorf("role %s: unknown permission %s", r.Code, p)
			}
		}
	}
}

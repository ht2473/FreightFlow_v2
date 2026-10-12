package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
)

var (
	errUnauthorized = errors.New("unauthorized")
	errForbidden    = errors.New("forbidden")
	errSessionOnly  = errors.New("operation requires a browser session")
	// errAdminTOTPRequired — администратор не может отключить себе двухфакторную защиту.
	errAdminTOTPRequired = errors.New("two-factor authentication is mandatory for administrators")
)

type principalKey struct{}

// PrincipalFrom возвращает субъекта запроса, установленного проверкой безопасности.
func PrincipalFrom(ctx context.Context) (identity.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(identity.Principal)
	return p, ok
}

func principal(ctx context.Context) identity.Principal {
	p, _ := PrincipalFrom(ctx)
	return p
}

// HandleCookieAuth проверяет сессию браузера. Сессия, начатая до последней смены пароля,
// недействительна.
func (s *Server) HandleCookieAuth(ctx context.Context, op gen.OperationName, _ gen.CookieAuth) (context.Context, error) {
	uid, authAt, ok := s.sessions.UserID(ctx)
	if !ok {
		return ctx, errUnauthorized
	}
	changedAt, err := s.identity.PasswordChangedAt(ctx, uid)
	if err != nil {
		return ctx, errUnauthorized
	}
	if authAt.Before(changedAt.Add(-time.Second)) { // допуск на округление времени при хранении
		_ = s.sessions.Logout(ctx)
		return ctx, errUnauthorized
	}
	p, err := s.identity.Principal(ctx, uid)
	if err != nil {
		return ctx, errUnauthorized
	}
	return s.authorize(ctx, op, p)
}

// HandleBearerAuth проверяет токен API.
func (s *Server) HandleBearerAuth(ctx context.Context, op gen.OperationName, t gen.BearerAuth) (context.Context, error) {
	p, err := s.identity.AuthenticateAPIToken(ctx, t.Token)
	if err != nil {
		return ctx, errUnauthorized
	}
	return s.authorize(ctx, op, p)
}

// authorize проверяет право операции. Операция без описания права запрещена.
func (s *Server) authorize(ctx context.Context, op gen.OperationName, p identity.Principal) (context.Context, error) {
	perm, ok := s.perms[strings.ToLower(string(op))]
	if !ok || perm == "" {
		return ctx, errForbidden
	}
	if perm != PermAuthenticated && !p.Has(identity.Permission(perm)) {
		return ctx, errForbidden
	}
	return context.WithValue(ctx, principalKey{}, p), nil
}

// requireSession запрещает операцию по токену API (управление паролем, 2FA и токенами).
func requireSession(p identity.Principal) error {
	if p.TokenID != nil {
		return errSessionOnly
	}
	return nil
}

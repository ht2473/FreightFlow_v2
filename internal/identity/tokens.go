package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/identity/sqlc"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/secret"
)

// tokenPrefix отличает токены системы в журналах и при утечке в открытый доступ.
const tokenPrefix = "ffv2_"

// APIToken — токен доступа к API (без секретного значения).
type APIToken struct {
	ID         uuid.UUID
	Name       string
	Prefix     string
	Scopes     []Permission
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// CreateAPIToken выпускает токен с областями доступа из прав владельца и возвращает секретное
// значение — оно показывается один раз.
func (s *Service) CreateAPIToken(ctx context.Context, owner Principal, name string, scopes []Permission, expiresAt *time.Time, meta RequestMeta) (string, APIToken, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 100 {
		return "", APIToken{}, &ValidationError{Field: "name", Message: "Укажите название токена (до 100 символов)"}
	}
	if len(scopes) == 0 {
		return "", APIToken{}, &ValidationError{Field: "scopes", Message: "Выберите хотя бы одну область доступа"}
	}
	codes := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		if !owner.Has(sc) {
			return "", APIToken{}, &ValidationError{Field: "scopes", Message: "Нельзя выдать токену право, которого нет у владельца: " + string(sc)}
		}
		codes = append(codes, string(sc))
	}
	if expiresAt != nil && !expiresAt.After(s.now()) {
		return "", APIToken{}, &ValidationError{Field: "expiresAt", Message: "Срок действия должен быть в будущем"}
	}

	plain := tokenPrefix + secret.Token(32)
	row, err := s.q.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID: ids.New(), UserID: owner.UserID, Name: name, Prefix: plain[:12],
		TokenHash: secret.Hash(plain), Scopes: codes, ExpiresAt: expiresAt,
	})
	if err != nil {
		return "", APIToken{}, fmt.Errorf("create token: %w", err)
	}
	if err := s.audit(ctx, s.q, &owner, AuditEntry{
		Action: "token.create", EntityType: "api_token", EntityID: row.ID.String(),
		Details: map[string]any{"name": name, "scopes": codes},
	}, meta); err != nil {
		return "", APIToken{}, err
	}
	return plain, toAPIToken(row), nil
}

// AuthenticateAPIToken проверяет токен и возвращает субъекта с правами в пределах областей токена.
func (s *Service) AuthenticateAPIToken(ctx context.Context, plain string) (Principal, error) {
	if !strings.HasPrefix(plain, tokenPrefix) {
		return Principal{}, ErrInvalidToken
	}
	t, err := s.q.GetActiveAPITokenByHash(ctx, secret.Hash(plain))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	}
	if err != nil {
		return Principal{}, fmt.Errorf("get token: %w", err)
	}
	if t.UserStatus != StatusActive {
		return Principal{}, ErrAccountBlocked
	}
	p, err := s.Principal(ctx, t.UserID)
	if err != nil {
		return Principal{}, err
	}
	scoped := p.Permissions[:0:0]
	for _, perm := range p.Permissions {
		if slices.Contains(t.Scopes, string(perm)) {
			scoped = append(scoped, perm)
		}
	}
	p.Permissions = scoped
	id := t.ID
	p.TokenID = &id
	if err := s.q.TouchAPIToken(ctx, t.ID); err != nil {
		return Principal{}, fmt.Errorf("touch token: %w", err)
	}
	return p, nil
}

// ListAPITokens возвращает токены пользователя.
func (s *Service) ListAPITokens(ctx context.Context, userID uuid.UUID) ([]APIToken, error) {
	rows, err := s.q.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	out := make([]APIToken, len(rows))
	for i, r := range rows {
		out[i] = toAPIToken(r)
	}
	return out, nil
}

// RevokeAPIToken отзывает токен владельца.
func (s *Service) RevokeAPIToken(ctx context.Context, owner Principal, id uuid.UUID, meta RequestMeta) error {
	n, err := s.q.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: id, UserID: owner.UserID})
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return s.audit(ctx, s.q, &owner, AuditEntry{Action: "token.revoke", EntityType: "api_token", EntityID: id.String()}, meta)
}

func toAPIToken(r sqlc.CoreApiToken) APIToken {
	return APIToken{
		ID: r.ID, Name: r.Name, Prefix: r.Prefix, Scopes: toPermissions(r.Scopes),
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt,
	}
}

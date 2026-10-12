package tiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maypok86/otter/v2"

	"freightflow/internal/identity"
)

// Identity — проверка учётных записей (модуль доступа).
type Identity interface {
	Principal(ctx context.Context, userID uuid.UUID) (identity.Principal, error)
	PasswordChangedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
	AuthenticateAPIToken(ctx context.Context, plain string) (identity.Principal, error)
}

// Sessions — сессии браузера.
type Sessions interface {
	UserID(ctx context.Context) (uuid.UUID, time.Time, bool)
}

// Auth определяет субъекта запроса тайла по сессии или токену API. Карта запрашивает
// десятки тайлов за раз, поэтому субъект кешируется на короткий срок.
type Auth struct {
	ids      Identity
	sessions Sessions
	cache    *otter.Cache[string, identity.Principal]
}

// principalTTL — сколько помнить субъекта; изменение ролей вступает в силу не позже.
const principalTTL = 30 * time.Second

// NewAuth создаёт проверку доступа к тайлам.
func NewAuth(ids Identity, sessions Sessions) *Auth {
	return &Auth{
		ids: ids, sessions: sessions,
		cache: otter.Must(&otter.Options[string, identity.Principal]{
			MaximumSize:      10000,
			ExpiryCalculator: otter.ExpiryWriting[string, identity.Principal](principalTTL),
		}),
	}
}

// Principal возвращает субъекта запроса. Сессия, начатая до смены пароля, недействительна.
func (a *Auth) Principal(r *http.Request) (identity.Principal, bool) {
	ctx := r.Context()
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		sum := sha256.Sum256([]byte(token))
		key := "t:" + hex.EncodeToString(sum[:])
		if p, ok := a.cache.GetIfPresent(key); ok {
			return p, true
		}
		p, err := a.ids.AuthenticateAPIToken(ctx, token)
		if err != nil {
			return identity.Principal{}, false
		}
		a.cache.Set(key, p)
		return p, true
	}
	uid, authAt, ok := a.sessions.UserID(ctx)
	if !ok {
		return identity.Principal{}, false
	}
	key := "s:" + uid.String() + ":" + authAt.UTC().Format(time.RFC3339Nano)
	if p, ok := a.cache.GetIfPresent(key); ok {
		return p, true
	}
	changedAt, err := a.ids.PasswordChangedAt(ctx, uid)
	if err != nil || authAt.Before(changedAt.Add(-time.Second)) {
		return identity.Principal{}, false
	}
	p, err := a.ids.Principal(ctx, uid)
	if err != nil {
		return identity.Principal{}, false
	}
	a.cache.Set(key, p)
	return p, true
}

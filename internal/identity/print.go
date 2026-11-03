package identity

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/binary"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// printPrefix — токен печати отчёта: открывает страницы отчёта в службе PDF от имени
// пользователя без сессии и без хранения в базе.
const printPrefix = "ffp_"

// printPermissions — права, которые даёт токен печати (только просмотр, пересечение с правами
// пользователя).
var printPermissions = []Permission{
	PermMapView, PermAnalyticsView, PermReportsView, PermRoutingUse, PermScenariosManage, PermAlertsOwn, PermDataManage,
}

// IssuePrintToken выдаёт токен печати на ttl: идентификатор пользователя и срок, подписанные
// ключом приложения.
func (s *Service) IssuePrintToken(userID uuid.UUID, ttl time.Duration) string {
	payload := make([]byte, 24)
	copy(payload, userID[:])
	binary.BigEndian.PutUint64(payload[16:], uint64(s.now().Add(ttl).Unix())) //nolint:gosec // время после 1970 года
	return printPrefix + base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(s.box.MAC(payload))
}

// printPrincipal проверяет токен печати и возвращает субъекта только для чтения.
func (s *Service) printPrincipal(ctx context.Context, token string) (Principal, error) {
	body, sig, ok := strings.Cut(strings.TrimPrefix(token, printPrefix), ".")
	if !ok {
		return Principal{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(payload) != 24 {
		return Principal{}, ErrInvalidToken
	}
	mac, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(mac, s.box.MAC(payload)) {
		return Principal{}, ErrInvalidToken
	}
	if s.now().Unix() > int64(binary.BigEndian.Uint64(payload[16:])) { //nolint:gosec // срок из подписанного токена
		return Principal{}, ErrInvalidToken
	}
	uid, err := uuid.FromBytes(payload[:16])
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	u, err := s.GetUser(ctx, uid)
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	if u.Status != StatusActive {
		return Principal{}, ErrAccountBlocked
	}
	p, err := s.Principal(ctx, uid)
	if err != nil {
		return Principal{}, err
	}
	p.Permissions = slices.DeleteFunc(p.Permissions, func(perm Permission) bool { return !slices.Contains(printPermissions, perm) })
	p.ReadOnly = true
	return p, nil
}

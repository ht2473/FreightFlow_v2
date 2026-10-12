// Package identity — учётные записи, роли и права, аутентификация, токены API и журнал аудита.
package identity

import (
	"errors"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
)

// User — учётная запись пользователя.
type User struct {
	ID          uuid.UUID
	Email       string
	Name        string
	Status      string
	Roles       []string
	TOTPEnabled bool
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

// Статусы учётной записи.
const (
	StatusActive  = "active"
	StatusBlocked = "blocked"
)

// Principal — субъект запроса: пользователь с вычисленными правами. Для запросов по токену API
// права ограничены областями токена.
type Principal struct {
	UserID      uuid.UUID
	Email       string
	Name        string
	Roles       []string
	Permissions []Permission
	TokenID     *uuid.UUID
}

// Has сообщает, есть ли у субъекта право.
func (p Principal) Has(perm Permission) bool {
	return slices.Contains(p.Permissions, perm)
}

// Label — подпись субъекта для журнала аудита.
func (p Principal) Label() string {
	if p.Name != "" {
		return p.Name + " <" + p.Email + ">"
	}
	return p.Email
}

// RequestMeta — сведения о запросе для журнала аудита и защиты от подбора.
type RequestMeta struct {
	IP        netip.Addr
	UserAgent string
}

// ValidationError — ошибка проверки входных данных с привязкой к полю.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// Ошибки модуля.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account temporarily locked")
	ErrAccountBlocked     = errors.New("account blocked")
	ErrInvalidCode        = errors.New("invalid second factor code")
	ErrNotFound           = errors.New("not found")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrLastAdmin          = errors.New("cannot remove the last administrator")
	ErrTOTPEnabled        = errors.New("second factor already enabled")
)

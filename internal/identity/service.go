package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/identity/sqlc"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/secret"
)

// Политика входа.
const (
	maxLoginAttempts  = 5
	lockMinutes       = 15
	inviteTTL         = 72 * time.Hour
	passwordResetTTL  = time.Hour
	recoveryCodeCount = 10
)

// Service — сценарии модуля доступа.
type Service struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
	box  *secret.Box
	now  func() time.Time
}

// NewService создаёт сервис. box шифрует секреты TOTP.
func NewService(pool *pgxpool.Pool, box *secret.Box) *Service {
	return &Service{pool: pool, q: sqlc.New(pool), box: box, now: time.Now}
}

// SyncBuiltinRoles приводит встроенные роли и их права в базе в соответствие с кодом.
func (s *Service) SyncBuiltinRoles(ctx context.Context) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		for _, r := range BuiltinRoles {
			if err := q.UpsertRole(ctx, sqlc.UpsertRoleParams{
				Code: r.Code, Name: r.Name, Description: r.Description, Builtin: true,
			}); err != nil {
				return fmt.Errorf("upsert role %s: %w", r.Code, err)
			}
			if err := q.DeleteRolePermissions(ctx, r.Code); err != nil {
				return fmt.Errorf("reset permissions of %s: %w", r.Code, err)
			}
			for _, p := range r.Permissions {
				if err := q.InsertRolePermission(ctx, sqlc.InsertRolePermissionParams{
					RoleCode: r.Code, Permission: string(p),
				}); err != nil {
					return fmt.Errorf("grant %s to %s: %w", p, r.Code, err)
				}
			}
		}
		return nil
	})
}

// CreateUserInput — данные новой учётной записи.
type CreateUserInput struct {
	Email    string
	Name     string
	Password string
	Roles    []string
}

// CreateUser создаёт учётную запись. actor — кто создаёт (nil для командной строки).
func (s *Service) CreateUser(ctx context.Context, actor *Principal, in CreateUserInput, meta RequestMeta) (User, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return User{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return User{}, err
	}
	hash := HashPassword(in.Password)

	var created sqlc.CoreAppUser
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := s.checkRoles(ctx, q, in.Roles); err != nil {
			return err
		}
		u, err := q.CreateUser(ctx, sqlc.CreateUserParams{
			ID: ids.New(), Email: email, Name: name, PasswordHash: &hash, Status: StatusActive,
		})
		if err != nil {
			return mapUniqueEmail(err)
		}
		for _, r := range in.Roles {
			if err := q.AddUserRole(ctx, sqlc.AddUserRoleParams{UserID: u.ID, RoleCode: r}); err != nil {
				return fmt.Errorf("add role: %w", err)
			}
		}
		created = u
		return s.audit(ctx, q, actor, AuditEntry{
			Action: "user.create", EntityType: "user", EntityID: u.ID.String(),
			Details: map[string]any{"email": email, "roles": in.Roles},
		}, meta)
	})
	if err != nil {
		return User{}, err
	}
	return toUser(created, in.Roles), nil
}

// LoginResult — итог проверки пароля.
type LoginResult struct {
	UserID            uuid.UUID
	NeedsSecondFactor bool
}

// Authenticate проверяет email и пароль. Если у пользователя включена 2FA, вход завершается
// после VerifySecondFactor.
func (s *Service) Authenticate(ctx context.Context, email, password string, meta RequestMeta) (LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _, _ = VerifyPassword(password, dummyHash())
		_ = s.audit(ctx, s.q, nil, AuditEntry{Action: "auth.login_failed", Details: map[string]any{"email": email, "reason": "unknown_email"}}, meta)
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("get user: %w", err)
	}
	if u.LockedUntil != nil && u.LockedUntil.After(s.now()) {
		return LoginResult{}, ErrAccountLocked
	}
	if u.PasswordHash == nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	ok, stale, err := VerifyPassword(password, *u.PasswordHash)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		if err := s.recordFailure(ctx, u, "wrong_password", meta); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, ErrInvalidCredentials
	}
	if u.Status != StatusActive {
		return LoginResult{}, ErrAccountBlocked
	}
	if stale {
		h := HashPassword(password)
		if err := s.q.RehashPassword(ctx, sqlc.RehashPasswordParams{ID: u.ID, PasswordHash: &h}); err != nil {
			return LoginResult{}, fmt.Errorf("rehash password: %w", err)
		}
	}
	if u.TotpEnabled {
		return LoginResult{UserID: u.ID, NeedsSecondFactor: true}, nil
	}
	return LoginResult{UserID: u.ID}, s.recordSuccess(ctx, u, "password", meta)
}

// VerifySecondFactor проверяет код TOTP или резервный код и завершает вход.
func (s *Service) VerifySecondFactor(ctx context.Context, userID uuid.UUID, code string, meta RequestMeta) error {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return mapNotFound(err)
	}
	if u.Status != StatusActive {
		return ErrAccountBlocked
	}
	if u.LockedUntil != nil && u.LockedUntil.After(s.now()) {
		return ErrAccountLocked
	}
	if !u.TotpEnabled || u.TotpSecret == nil {
		return ErrInvalidCode
	}
	plain, err := s.box.Open(u.TotpSecret)
	if err != nil {
		return fmt.Errorf("open totp secret: %w", err)
	}
	if validateTOTP(code, string(plain), s.now()) {
		return s.recordSuccess(ctx, u, "totp", meta)
	}
	n, err := s.q.UseRecoveryCode(ctx, sqlc.UseRecoveryCodeParams{
		UserID: u.ID, CodeHash: secret.Hash(normalizeRecoveryCode(code)),
	})
	if err != nil {
		return fmt.Errorf("use recovery code: %w", err)
	}
	if n == 1 {
		return s.recordSuccess(ctx, u, "recovery_code", meta)
	}
	if err := s.recordFailure(ctx, u, "wrong_second_factor", meta); err != nil {
		return err
	}
	return ErrInvalidCode
}

func (s *Service) recordSuccess(ctx context.Context, u sqlc.CoreAppUser, method string, meta RequestMeta) error {
	if err := s.q.RecordLoginSuccess(ctx, u.ID); err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	actor := &Principal{UserID: u.ID, Email: u.Email, Name: u.Name}
	return s.audit(ctx, s.q, actor, AuditEntry{
		Action: "auth.login", EntityType: "user", EntityID: u.ID.String(),
		Details: map[string]any{"method": method},
	}, meta)
}

func (s *Service) recordFailure(ctx context.Context, u sqlc.CoreAppUser, reason string, meta RequestMeta) error {
	res, err := s.q.RecordLoginFailure(ctx, sqlc.RecordLoginFailureParams{
		ID: u.ID, MaxAttempts: maxLoginAttempts, LockMinutes: lockMinutes,
	})
	if err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	details := map[string]any{"email": u.Email, "reason": reason, "failed_logins": res.FailedLogins}
	if res.LockedUntil != nil {
		details["locked_until"] = res.LockedUntil
	}
	return s.audit(ctx, s.q, nil, AuditEntry{Action: "auth.login_failed", EntityType: "user", EntityID: u.ID.String(), Details: details}, meta)
}

// Principal возвращает субъекта с правами для активного пользователя.
func (s *Service) Principal(ctx context.Context, userID uuid.UUID) (Principal, error) {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return Principal{}, mapNotFound(err)
	}
	if u.Status != StatusActive {
		return Principal{}, ErrAccountBlocked
	}
	roles, err := s.q.ListUserRoles(ctx, userID)
	if err != nil {
		return Principal{}, fmt.Errorf("list roles: %w", err)
	}
	perms, err := s.q.ListUserPermissions(ctx, userID)
	if err != nil {
		return Principal{}, fmt.Errorf("list permissions: %w", err)
	}
	return Principal{
		UserID: u.ID, Email: u.Email, Name: u.Name, Roles: roles, Permissions: toPermissions(perms),
	}, nil
}

// PasswordChangedAt возвращает время последней смены пароля: сессии, начатые раньше, недействительны.
func (s *Service) PasswordChangedAt(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return time.Time{}, mapNotFound(err)
	}
	return u.PasswordChangedAt, nil
}

// BeginTOTPSetup создаёт новый секрет TOTP. 2FA включается после ConfirmTOTP.
func (s *Service) BeginTOTPSetup(ctx context.Context, userID uuid.UUID) (TOTPSetup, error) {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return TOTPSetup{}, mapNotFound(err)
	}
	// Новый секрет сбрасывает признак включения; при действующей защите
	// это было бы отключением 2FA без подтверждения паролем.
	if u.TotpEnabled {
		return TOTPSetup{}, ErrTOTPEnabled
	}
	key, err := generateTOTP(u.Email)
	if err != nil {
		return TOTPSetup{}, fmt.Errorf("generate totp: %w", err)
	}
	if err := s.q.SetTOTPSecret(ctx, sqlc.SetTOTPSecretParams{ID: u.ID, TotpSecret: s.box.Seal([]byte(key.Secret()))}); err != nil {
		return TOTPSetup{}, fmt.Errorf("store totp secret: %w", err)
	}
	return TOTPSetup{Secret: key.Secret(), URL: key.URL()}, nil
}

// ConfirmTOTP включает 2FA после проверки первого кода и возвращает резервные коды.
func (s *Service) ConfirmTOTP(ctx context.Context, userID uuid.UUID, code string, meta RequestMeta) ([]string, error) {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if u.TotpSecret == nil {
		return nil, ErrInvalidCode
	}
	plain, err := s.box.Open(u.TotpSecret)
	if err != nil {
		return nil, fmt.Errorf("open totp secret: %w", err)
	}
	if !validateTOTP(code, string(plain), s.now()) {
		return nil, ErrInvalidCode
	}
	codes := newRecoveryCodes(recoveryCodeCount)
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.EnableTOTP(ctx, u.ID); err != nil {
			return fmt.Errorf("enable totp: %w", err)
		}
		if err := q.DeleteRecoveryCodes(ctx, u.ID); err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		for _, c := range codes {
			if err := q.InsertRecoveryCode(ctx, sqlc.InsertRecoveryCodeParams{UserID: u.ID, CodeHash: secret.Hash(c)}); err != nil {
				return fmt.Errorf("insert recovery code: %w", err)
			}
		}
		actor := &Principal{UserID: u.ID, Email: u.Email, Name: u.Name}
		return s.audit(ctx, q, actor, AuditEntry{Action: "auth.totp_enabled", EntityType: "user", EntityID: u.ID.String()}, meta)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP отключает 2FA пользователя (сам пользователь или администратор).
func (s *Service) DisableTOTP(ctx context.Context, actor *Principal, userID uuid.UUID, meta RequestMeta) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.DisableTOTP(ctx, userID); err != nil {
			return fmt.Errorf("disable totp: %w", err)
		}
		if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		return s.audit(ctx, q, actor, AuditEntry{Action: "auth.totp_disabled", EntityType: "user", EntityID: userID.String()}, meta)
	})
}

// ChangePassword меняет пароль после проверки текущего.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, current, next string, meta RequestMeta) error {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return mapNotFound(err)
	}
	if u.PasswordHash == nil {
		return ErrInvalidCredentials
	}
	ok, _, err := VerifyPassword(current, *u.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return &ValidationError{Field: "currentPassword", Message: "Текущий пароль указан неверно"}
	}
	if err := ValidatePassword(next); err != nil {
		return err
	}
	h := HashPassword(next)
	if err := s.q.SetPasswordHash(ctx, sqlc.SetPasswordHashParams{ID: u.ID, PasswordHash: &h}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	actor := &Principal{UserID: u.ID, Email: u.Email, Name: u.Name}
	return s.audit(ctx, s.q, actor, AuditEntry{Action: "auth.password_changed", EntityType: "user", EntityID: u.ID.String()}, meta)
}

// CheckPassword подтверждает пароль пользователя перед чувствительным действием.
func (s *Service) CheckPassword(ctx context.Context, userID uuid.UUID, password string) error {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return mapNotFound(err)
	}
	if u.PasswordHash == nil {
		return ErrInvalidCredentials
	}
	ok, _, err := VerifyPassword(password, *u.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return nil
}

// NormalizeEmail приводит адрес к нижнему регистру и проверяет формат.
func NormalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || utf8.RuneCountInString(email) > 254 {
		return "", &ValidationError{Field: "email", Message: "Укажите корректный адрес электронной почты"}
	}
	return email, nil
}

func normalizeName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	n := utf8.RuneCountInString(name)
	if n == 0 || n > 200 {
		return "", &ValidationError{Field: "name", Message: "Укажите имя (до 200 символов)"}
	}
	return name, nil
}

func (s *Service) checkRoles(ctx context.Context, q *sqlc.Queries, roles []string) error {
	if len(roles) == 0 {
		return &ValidationError{Field: "roles", Message: "Назначьте хотя бы одну роль"}
	}
	for _, r := range roles {
		ok, err := q.RoleExists(ctx, r)
		if err != nil {
			return fmt.Errorf("check role: %w", err)
		}
		if !ok {
			return &ValidationError{Field: "roles", Message: "Неизвестная роль: " + r}
		}
	}
	return nil
}

func toUser(u sqlc.CoreAppUser, roles []string) User {
	return User{
		ID: u.ID, Email: u.Email, Name: u.Name, Status: u.Status, Roles: roles,
		TOTPEnabled: u.TotpEnabled, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
	}
}

func toPermissions(codes []string) []Permission {
	out := make([]Permission, len(codes))
	for i, c := range codes {
		out[i] = Permission(c)
	}
	return out
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func mapUniqueEmail(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrEmailTaken
	}
	return fmt.Errorf("create user: %w", err)
}

// dummyHash вычисляется один раз при первом обращении: проверка пароля для несуществующей
// учётной записи занимает то же время, что и для существующей.
var dummyHash = sync.OnceValue(func() string {
	return HashPassword("timing equalization for unknown accounts")
})

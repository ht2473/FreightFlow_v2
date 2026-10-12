package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/identity/sqlc"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/secret"
)

// Role — роль с набором прав.
type Role struct {
	Code        string
	Name        string
	Description string
	Builtin     bool
	Permissions []Permission
}

// ListRoles возвращает роли с правами.
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.q.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	out := make([]Role, len(rows))
	for i, r := range rows {
		out[i] = Role{Code: r.Code, Name: r.Name, Description: r.Description, Builtin: r.Builtin, Permissions: toPermissions(r.Permissions)}
	}
	return out, nil
}

// ListUsers возвращает все учётные записи с ролями.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = User{
			ID: r.ID, Email: r.Email, Name: r.Name, Status: r.Status, Roles: r.Roles,
			TOTPEnabled: r.TotpEnabled, LastLoginAt: r.LastLoginAt, CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

// GetUser возвращает учётную запись.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		return User{}, mapNotFound(err)
	}
	roles, err := s.q.ListUserRoles(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("list roles: %w", err)
	}
	return toUser(u, roles), nil
}

// UpdateProfile меняет имя пользователя.
func (s *Service) UpdateProfile(ctx context.Context, actor Principal, userID uuid.UUID, name string, meta RequestMeta) (User, error) {
	name, err := normalizeName(name)
	if err != nil {
		return User{}, err
	}
	u, err := s.q.UpdateUserProfile(ctx, sqlc.UpdateUserProfileParams{ID: userID, Name: name})
	if err != nil {
		return User{}, mapNotFound(err)
	}
	if err := s.audit(ctx, s.q, &actor, AuditEntry{Action: "user.update_profile", EntityType: "user", EntityID: userID.String()}, meta); err != nil {
		return User{}, err
	}
	roles, err := s.q.ListUserRoles(ctx, userID)
	if err != nil {
		return User{}, fmt.Errorf("list roles: %w", err)
	}
	return toUser(u, roles), nil
}

// SetUserRoles заменяет роли пользователя. Последнего активного администратора лишить роли нельзя.
func (s *Service) SetUserRoles(ctx context.Context, actor Principal, userID uuid.UUID, roles []string, meta RequestMeta) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := s.checkRoles(ctx, q, roles); err != nil {
			return err
		}
		current, err := q.ListUserRoles(ctx, userID)
		if err != nil {
			return fmt.Errorf("list roles: %w", err)
		}
		if slices.Contains(current, RoleAdmin) && !slices.Contains(roles, RoleAdmin) {
			if err := s.ensureOtherAdmin(ctx, q); err != nil {
				return err
			}
		}
		if err := q.DeleteUserRoles(ctx, userID); err != nil {
			return fmt.Errorf("delete roles: %w", err)
		}
		for _, r := range roles {
			if err := q.AddUserRole(ctx, sqlc.AddUserRoleParams{UserID: userID, RoleCode: r}); err != nil {
				return fmt.Errorf("add role: %w", err)
			}
		}
		return s.audit(ctx, q, &actor, AuditEntry{
			Action: "user.set_roles", EntityType: "user", EntityID: userID.String(),
			Details: map[string]any{"from": current, "to": roles},
		}, meta)
	})
}

// SetUserStatus блокирует или разблокирует учётную запись.
func (s *Service) SetUserStatus(ctx context.Context, actor Principal, userID uuid.UUID, status string, meta RequestMeta) error {
	if status != StatusActive && status != StatusBlocked {
		return &ValidationError{Field: "status", Message: "Недопустимый статус"}
	}
	if status == StatusBlocked && actor.UserID == userID {
		return &ValidationError{Field: "status", Message: "Нельзя заблокировать собственную учётную запись"}
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if status == StatusBlocked {
			roles, err := q.ListUserRoles(ctx, userID)
			if err != nil {
				return fmt.Errorf("list roles: %w", err)
			}
			if slices.Contains(roles, RoleAdmin) {
				if err := s.ensureOtherAdmin(ctx, q); err != nil {
					return err
				}
			}
		}
		if _, err := q.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{ID: userID, Status: status}); err != nil {
			return mapNotFound(err)
		}
		return s.audit(ctx, q, &actor, AuditEntry{
			Action: "user.set_status", EntityType: "user", EntityID: userID.String(),
			Details: map[string]any{"status": status},
		}, meta)
	})
}

func (s *Service) ensureOtherAdmin(ctx context.Context, q *sqlc.Queries) error {
	n, err := q.CountActiveUsersWithRole(ctx, RoleAdmin)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// Invite — действующее приглашение.
type Invite struct {
	ID        uuid.UUID
	Email     string
	Role      string
	ExpiresAt string
}

// CreateInvite создаёт приглашение и возвращает одноразовый токен для ссылки в письме.
func (s *Service) CreateInvite(ctx context.Context, actor Principal, email, role string, meta RequestMeta) (string, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return "", err
	}
	if _, err := s.q.GetUserByEmail(ctx, email); err == nil {
		return "", ErrEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("check email: %w", err)
	}
	if err := s.checkRoles(ctx, s.q, []string{role}); err != nil {
		return "", err
	}
	token := secret.Token(32)
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		createdBy := actor.UserID
		if _, err := q.CreateOneTimeToken(ctx, sqlc.CreateOneTimeTokenParams{
			ID: ids.New(), Purpose: "invite", TokenHash: secret.Hash(token), Email: email,
			RoleCode: &role, CreatedBy: &createdBy, ExpiresAt: s.now().Add(inviteTTL),
		}); err != nil {
			return fmt.Errorf("create invite: %w", err)
		}
		return s.audit(ctx, q, &actor, AuditEntry{
			Action: "user.invite", EntityType: "invite",
			Details: map[string]any{"email": email, "role": role},
		}, meta)
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// InviteEmail возвращает адрес действующего приглашения (для формы принятия).
func (s *Service) InviteEmail(ctx context.Context, token string) (string, error) {
	t, err := s.q.GetValidOneTimeToken(ctx, sqlc.GetValidOneTimeTokenParams{TokenHash: secret.Hash(token), Purpose: "invite"})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidToken
		}
		return "", fmt.Errorf("get invite: %w", err)
	}
	return t.Email, nil
}

// AcceptInvite создаёт учётную запись по приглашению.
func (s *Service) AcceptInvite(ctx context.Context, token, name, password string, meta RequestMeta) (User, error) {
	nm, err := normalizeName(name)
	if err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	hash := HashPassword(password)

	var created sqlc.CoreAppUser
	var role string
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		t, err := q.GetValidOneTimeToken(ctx, sqlc.GetValidOneTimeTokenParams{TokenHash: secret.Hash(token), Purpose: "invite"})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidToken
			}
			return fmt.Errorf("get invite: %w", err)
		}
		if n, err := q.MarkOneTimeTokenUsed(ctx, t.ID); err != nil || n != 1 {
			return ErrInvalidToken
		}
		u, err := q.CreateUser(ctx, sqlc.CreateUserParams{ID: ids.New(), Email: t.Email, Name: nm, PasswordHash: &hash, Status: StatusActive})
		if err != nil {
			return mapUniqueEmail(err)
		}
		if t.RoleCode != nil {
			role = *t.RoleCode
			if err := q.AddUserRole(ctx, sqlc.AddUserRoleParams{UserID: u.ID, RoleCode: role}); err != nil {
				return fmt.Errorf("add role: %w", err)
			}
		}
		created = u
		actor := &Principal{UserID: u.ID, Email: u.Email, Name: u.Name}
		return s.audit(ctx, q, actor, AuditEntry{Action: "user.invite_accepted", EntityType: "user", EntityID: u.ID.String()}, meta)
	})
	if err != nil {
		return User{}, err
	}
	return toUser(created, []string{role}), nil
}

// RequestPasswordReset создаёт токен сброса пароля. ok=false, если активной учётной записи с
// таким адресом нет: вызывающий отвечает одинаково в обоих случаях.
func (s *Service) RequestPasswordReset(ctx context.Context, email string, meta RequestMeta) (token string, u User, ok bool, err error) {
	email, err = NormalizeEmail(email)
	if err != nil {
		return "", User{}, false, err
	}
	row, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", User{}, false, nil
	}
	if err != nil {
		return "", User{}, false, fmt.Errorf("get user: %w", err)
	}
	if row.Status != StatusActive {
		return "", User{}, false, nil
	}
	token = secret.Token(32)
	uid := row.ID
	if _, err := s.q.CreateOneTimeToken(ctx, sqlc.CreateOneTimeTokenParams{
		ID: ids.New(), Purpose: "password_reset", TokenHash: secret.Hash(token), Email: email,
		UserID: &uid, ExpiresAt: s.now().Add(passwordResetTTL),
	}); err != nil {
		return "", User{}, false, fmt.Errorf("create reset token: %w", err)
	}
	if err := s.audit(ctx, s.q, nil, AuditEntry{Action: "auth.password_reset_requested", EntityType: "user", EntityID: uid.String()}, meta); err != nil {
		return "", User{}, false, err
	}
	return token, toUser(row, nil), true, nil
}

// ResetPassword задаёт новый пароль по токену сброса. Прежние сессии становятся недействительными.
func (s *Service) ResetPassword(ctx context.Context, token, password string, meta RequestMeta) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash := HashPassword(password)
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		t, err := q.GetValidOneTimeToken(ctx, sqlc.GetValidOneTimeTokenParams{TokenHash: secret.Hash(token), Purpose: "password_reset"})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidToken
			}
			return fmt.Errorf("get reset token: %w", err)
		}
		if t.UserID == nil {
			return ErrInvalidToken
		}
		if n, err := q.MarkOneTimeTokenUsed(ctx, t.ID); err != nil || n != 1 {
			return ErrInvalidToken
		}
		if err := q.SetPasswordHash(ctx, sqlc.SetPasswordHashParams{ID: *t.UserID, PasswordHash: &hash}); err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		return s.audit(ctx, q, nil, AuditEntry{Action: "auth.password_reset", EntityType: "user", EntityID: t.UserID.String()}, meta)
	})
}

// Settings — личные настройки пользователя.
type Settings struct {
	Theme  string          `json:"theme"`
	Extras json.RawMessage `json:"extras,omitempty"`
}

// GetSettings возвращает настройки пользователя (по умолчанию — системная тема).
func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (Settings, error) {
	row, err := s.q.GetUserSetting(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Theme: "system"}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("get settings: %w", err)
	}
	return Settings{Theme: row.Theme, Extras: row.Settings}, nil
}

// UpdateSettings сохраняет настройки пользователя.
func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, in Settings) (Settings, error) {
	switch in.Theme {
	case "light", "dark", "system":
	default:
		return Settings{}, &ValidationError{Field: "theme", Message: "Тема: light, dark или system"}
	}
	extras := in.Extras
	if len(extras) == 0 {
		extras = json.RawMessage(`{}`)
	}
	row, err := s.q.UpsertUserSetting(ctx, sqlc.UpsertUserSettingParams{UserID: userID, Theme: in.Theme, Settings: extras})
	if err != nil {
		return Settings{}, fmt.Errorf("save settings: %w", err)
	}
	return Settings{Theme: row.Theme, Extras: row.Settings}, nil
}

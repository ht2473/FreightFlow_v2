// Package session управляет сессиями браузера: хранение в PostgreSQL, вход, выход, второй фактор.
package session

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CookieName — имя cookie сессии (совпадает со схемой cookieAuth в OpenAPI).
const CookieName = "ff_session"

const (
	keyUserID     = "uid"
	keyAuthAt     = "auth_at"
	keyPendingUID = "pending_uid"
	keyPendingAt  = "pending_at"

	// pendingTTL — сколько ждать второй фактор после верного пароля.
	pendingTTL = 5 * time.Minute
)

func init() {
	// Значения сессии кодируются gob: тип времени входа нужно зарегистрировать.
	gob.Register(time.Time{})
}

// Manager — сессии пользователей.
type Manager struct {
	*scs.SessionManager
}

// New создаёт менеджер сессий. secure включает флаг Secure у cookie (HTTPS).
func New(pool *pgxpool.Pool, secure bool) *Manager {
	m := scs.New()
	m.Store = &pgStore{pool: pool}
	m.Lifetime = 7 * 24 * time.Hour
	m.IdleTimeout = 8 * time.Hour
	m.Cookie.Name = CookieName
	m.Cookie.HttpOnly = true
	m.Cookie.SameSite = http.SameSiteLaxMode
	m.Cookie.Secure = secure
	m.Cookie.Path = "/"
	// Клиент прервал запрос (ушёл со страницы, отменил загрузку тайла) — ответ уже никому
	// не нужен, это не ошибка сервера.
	m.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, context.Canceled) {
			return
		}
		slog.ErrorContext(r.Context(), "session store", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
	return &Manager{SessionManager: m}
}

// Login начинает аутентифицированную сессию. Токен сессии меняется, чтобы исключить фиксацию сессии.
func (m *Manager) Login(ctx context.Context, userID uuid.UUID) error {
	if err := m.RenewToken(ctx); err != nil {
		return fmt.Errorf("renew session token: %w", err)
	}
	m.Remove(ctx, keyPendingUID)
	m.Remove(ctx, keyPendingAt)
	m.Put(ctx, keyUserID, userID.String())
	m.Put(ctx, keyAuthAt, time.Now().UTC())
	return nil
}

// Logout завершает сессию.
func (m *Manager) Logout(ctx context.Context) error {
	return m.Destroy(ctx)
}

// UserID возвращает пользователя сессии и время входа.
func (m *Manager) UserID(ctx context.Context) (uuid.UUID, time.Time, bool) {
	raw := m.GetString(ctx, keyUserID)
	if raw == "" {
		return uuid.Nil, time.Time{}, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, time.Time{}, false
	}
	return id, m.GetTime(ctx, keyAuthAt), true
}

// SetPending запоминает пользователя, прошедшего проверку пароля и ожидающего второй фактор.
func (m *Manager) SetPending(ctx context.Context, userID uuid.UUID) {
	m.Put(ctx, keyPendingUID, userID.String())
	m.Put(ctx, keyPendingAt, time.Now().UTC())
}

// Pending возвращает пользователя, ожидающего второй фактор, если ожидание не истекло.
func (m *Manager) Pending(ctx context.Context) (uuid.UUID, bool) {
	raw := m.GetString(ctx, keyPendingUID)
	at := m.GetTime(ctx, keyPendingAt)
	if raw == "" || time.Since(at) > pendingTTL {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	return id, err == nil
}

// ClearPending сбрасывает ожидание второго фактора.
func (m *Manager) ClearPending(ctx context.Context) {
	m.Remove(ctx, keyPendingUID)
	m.Remove(ctx, keyPendingAt)
}

// pgStore хранит сессии в таблице core.session.
type pgStore struct {
	pool *pgxpool.Pool
}

func (s *pgStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	var data []byte
	err := s.pool.QueryRow(ctx,
		`SELECT data FROM core.session WHERE token = $1 AND expiry > now()`, token).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find session: %w", err)
	}
	return data, true, nil
}

func (s *pgStore) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO core.session (token, data, expiry) VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE SET data = EXCLUDED.data, expiry = EXCLUDED.expiry`,
		token, b, expiry)
	if err != nil {
		return fmt.Errorf("commit session: %w", err)
	}
	return nil
}

func (s *pgStore) DeleteCtx(ctx context.Context, token string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM core.session WHERE token = $1`, token); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *pgStore) AllCtx(ctx context.Context) (map[string][]byte, error) {
	rows, err := s.pool.Query(ctx, `SELECT token, data FROM core.session WHERE expiry > now()`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var token string
		var data []byte
		if err := rows.Scan(&token, &data); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out[token] = data
	}
	return out, rows.Err()
}

// Методы без контекста нужны для интерфейса scs.Store; используются только контекстные версии.
func (s *pgStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *pgStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

func (s *pgStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

// DeleteExpired удаляет истёкшие сессии (вызывается задачей обслуживания).
func DeleteExpired(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM core.session WHERE expiry < now()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

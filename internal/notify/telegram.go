package notify

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/outbound"
	"freightflow/internal/platform/secret"
)

// telegram — клиент Bot API. Токен входит в адрес запросов, поэтому ошибки транспорта
// не должны его раскрывать: в тексте ошибки остаётся только метод.
type telegram struct {
	base string
	http *outbound.Client
	mu   sync.Mutex
	name string // имя бота из getMe
}

// errBotBlocked — пользователь заблокировал бота или удалил чат.
var errBotBlocked = errors.New("notify: telegram bot is blocked by the user")

func newTelegram(ctx context.Context, api, token string, allowPrivate bool) (*telegram, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(api, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	hostname, _, _ := strings.Cut(host, ":")
	c, err := outbound.New(ctx, outbound.Config{
		Hosts: []string{hostname}, NoRetry: true, AllowPrivate: allowPrivate, Timeout: 40 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &telegram{base: strings.TrimSuffix(api, "/") + "/bot" + token + "/", http: c}, nil
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

func (t *telegram) call(ctx context.Context, method string, args, out any) error {
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	resp, err := t.http.PostJSON(ctx, t.base+method, body)
	if err != nil {
		code := outbound.StatusCode(err)
		if code == http.StatusForbidden {
			return errBotBlocked
		}
		if code > 0 {
			return fmt.Errorf("telegram %s: status %d", method, code)
		}
		return fmt.Errorf("telegram %s: request failed", method)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	var r tgResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	if !r.OK {
		if r.ErrorCode == http.StatusForbidden {
			return errBotBlocked
		}
		return fmt.Errorf("telegram %s: %d %s", method, r.ErrorCode, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (t *telegram) send(ctx context.Context, chat int64, text string) error {
	return t.call(ctx, "sendMessage", map[string]any{
		"chat_id": chat, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true},
	}, nil)
}

// botName — имя бота (для ссылки t.me), запрашивается один раз.
func (t *telegram) botName(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.name != "" {
		return t.name, nil
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := t.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return "", err
	}
	t.name = me.Username
	return t.name, nil
}

// TelegramStatus — привязка Telegram пользователя.
type TelegramStatus struct {
	Configured bool
	Bound      bool
	Username   *string
	BoundAt    *time.Time
}

// TelegramStatus сообщает, настроен ли бот и привязан ли чат пользователя.
func (s *Service) TelegramStatus(ctx context.Context, userID uuid.UUID) (TelegramStatus, error) {
	st := TelegramStatus{Configured: s.telegram != nil}
	err := s.pool.QueryRow(ctx, `SELECT username, bound_at FROM notify.telegram_binding WHERE user_id = $1`, userID).
		Scan(&st.Username, &st.BoundAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Bound = true
	return st, nil
}

// TelegramLink — одноразовый код привязки и ссылка на бота.
type TelegramLink struct {
	Code      string
	URL       string
	ExpiresAt time.Time
}

// codeTTL — срок действия кода привязки.
const codeTTL = 15 * time.Minute

// LinkTelegram выдаёт код привязки: пользователь отправляет боту «/start код» (ссылка открывает
// бота с кодом). Хранится только хеш кода.
func (s *Service) LinkTelegram(ctx context.Context, userID uuid.UUID) (*TelegramLink, error) {
	if s.telegram == nil {
		return nil, ErrTelegramDisabled
	}
	name, err := s.telegram.botName(ctx)
	if err != nil {
		return nil, err
	}
	code := secret.Token(9)
	expires := s.now().Add(codeTTL)
	if _, err := s.pool.Exec(ctx, `DELETE FROM notify.telegram_code WHERE user_id = $1 OR expires_at < now()`, userID); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO notify.telegram_code (code_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hex.EncodeToString(secret.Hash(code)), userID, expires); err != nil {
		return nil, fmt.Errorf("telegram code: %w", err)
	}
	return &TelegramLink{Code: code, URL: "https://t.me/" + name + "?start=" + code, ExpiresAt: expires}, nil
}

// UnlinkTelegram отвязывает чат.
func (s *Service) UnlinkTelegram(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notify.telegram_binding WHERE user_id = $1`, userID)
	return err
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID       int64  `json:"id"`
			Type     string `json:"type"`
			Username string `json:"username"`
		} `json:"chat"`
	} `json:"message"`
}

// pollLock — номер блокировки, под которой обновления бота читает один процесс.
const pollLock = 7710042

// PollTelegram читает обновления бота, пока не отменён ctx: «/start код» привязывает чат,
// «/stop» отвязывает. Читает один процесс (рекомендательная блокировка сессии); позиция
// хранится в базе. Без токена бота сразу возвращается.
func (s *Service) PollTelegram(ctx context.Context) error {
	if s.telegram == nil {
		return nil
	}
	for {
		err := s.pollOnce(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // остановка процесса
		}
		if err != nil {
			s.log.WarnContext(ctx, "telegram polling failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

func (s *Service) pollOnce(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, pollLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil // читает другой процесс
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, pollLock) }()
	for ctx.Err() == nil {
		if err := s.PollStep(ctx, 25); err != nil {
			return err
		}
	}
	return nil
}

// PollStep читает одну порцию обновлений (длинный опрос до wait секунд) и обрабатывает их.
func (s *Service) PollStep(ctx context.Context, wait int) error {
	var offset int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO notify.telegram_state (id) VALUES (true)
		ON CONFLICT (id) DO UPDATE SET id = true RETURNING offset_id`).Scan(&offset); err != nil {
		return err
	}
	var updates []tgUpdate
	if err := s.telegram.call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": wait, "allowed_updates": []string{"message"},
	}, &updates); err != nil {
		return err
	}
	for _, u := range updates {
		if u.Message != nil && u.Message.Chat.Type == "private" {
			reply := s.handleCommand(ctx, u.Message.Chat.ID, u.Message.Chat.Username, strings.TrimSpace(u.Message.Text))
			if reply != "" {
				if err := s.telegram.send(ctx, u.Message.Chat.ID, reply); err != nil && !errors.Is(err, errBotBlocked) {
					s.log.WarnContext(ctx, "telegram reply failed", "err", err)
				}
			}
		}
		if _, err := s.pool.Exec(ctx, `UPDATE notify.telegram_state SET offset_id = $1`, u.UpdateID+1); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleCommand(ctx context.Context, chat int64, username, text string) string {
	cmd, arg, _ := strings.Cut(text, " ")
	switch cmd {
	case "/start":
		if arg == "" {
			return "Здравствуйте! Чтобы получать оповещения FreightFlow, откройте профиль в системе и нажмите «Привязать Telegram»."
		}
		var userID uuid.UUID
		err := s.pool.QueryRow(ctx, `DELETE FROM notify.telegram_code WHERE code_hash = $1 AND expires_at > now() RETURNING user_id`,
			hex.EncodeToString(secret.Hash(strings.TrimSpace(arg)))).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "Код не подошёл или устарел. Получите новый в профиле FreightFlow."
		}
		if err != nil {
			s.log.WarnContext(ctx, "telegram code", "err", err)
			return "Не удалось привязать чат, попробуйте позже."
		}
		var name *string
		if username != "" {
			name = &username
		}
		// Чат привязан к одному пользователю: прежняя привязка этого чата снимается.
		if _, err := s.pool.Exec(ctx, `DELETE FROM notify.telegram_binding WHERE chat_id = $1 AND user_id <> $2`, chat, userID); err != nil {
			s.log.WarnContext(ctx, "telegram unbind previous", "err", err)
			return "Не удалось привязать чат, попробуйте позже."
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO notify.telegram_binding (user_id, chat_id, username) VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET chat_id = $2, username = $3, bound_at = now()`, userID, chat, name); err != nil {
			s.log.WarnContext(ctx, "telegram bind", "err", err)
			return "Не удалось привязать чат, попробуйте позже."
		}
		return "Готово: оповещения FreightFlow будут приходить сюда. Отвязать — командой /stop или в профиле."
	case "/stop":
		if _, err := s.pool.Exec(ctx, `DELETE FROM notify.telegram_binding WHERE chat_id = $1`, chat); err != nil {
			return "Не удалось отвязать чат, попробуйте позже."
		}
		return "Чат отвязан, оповещения больше не придут."
	}
	return ""
}

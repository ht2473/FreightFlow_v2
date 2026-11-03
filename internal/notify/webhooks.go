package notify

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/secret"
)

// Webhook — вебхук пользователя (без секрета).
type Webhook struct {
	ID        uuid.UUID
	Name      string
	URL       string
	Enabled   bool
	CreatedAt time.Time
	// LastStatus — статус последней доставки (sent, failed, pending).
	LastStatus *string
	LastAt     *time.Time
}

// Delivery — запись журнала доставок вебхука.
type Delivery struct {
	ID           uuid.UUID
	Event        string
	Status       string
	Attempts     int
	ResponseCode *int
	LastError    *string
	CreatedAt    time.Time
	SentAt       *time.Time
}

// maxWebhooks — вебхуков у пользователя.
const maxWebhooks = 5

// checkURL проверяет адрес вебхука: HTTPS, имя хоста, не адрес локальной сети (адрес по имени
// проверяется ещё раз при соединении — имя может указывать во внутреннюю сеть).
func (s *Service) checkURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", invalid("неверный адрес вебхука")
	}
	if u.User != nil {
		return "", invalid("адрес вебхука не должен содержать логин и пароль")
	}
	if u.Scheme != "https" && (!s.cfg.AllowPrivateWebhooks || u.Scheme != "http") {
		return "", invalid("вебхук принимается только по HTTPS")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil && !s.cfg.AllowPrivateWebhooks {
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return "", invalid("адрес вебхука во внутренней сети не допускается")
		}
	}
	if strings.EqualFold(host, "localhost") && !s.cfg.AllowPrivateWebhooks {
		return "", invalid("адрес вебхука во внутренней сети не допускается")
	}
	u.Fragment = ""
	return u.String(), nil
}

// Webhooks — вебхуки пользователя со статусом последней доставки.
func (s *Service) Webhooks(ctx context.Context, userID uuid.UUID) ([]Webhook, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.name, w.url, w.enabled, w.created_at, d.status, coalesce(d.sent_at, d.created_at)
		FROM notify.webhook w
		LEFT JOIN LATERAL (SELECT status, sent_at, created_at FROM notify.delivery
			WHERE webhook_id = w.id ORDER BY created_at DESC LIMIT 1) d ON true
		WHERE w.user_id = $1 ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Webhook])
}

// CreateWebhook добавляет вебхук и возвращает его секрет подписи — единственный раз.
func (s *Service) CreateWebhook(ctx context.Context, userID uuid.UUID, name, rawURL string) (*Webhook, string, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n == 0 || n > 100 {
		return nil, "", invalid("название — от 1 до 100 символов")
	}
	u, err := s.checkURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notify.webhook WHERE user_id = $1`, userID).Scan(&count); err != nil {
		return nil, "", err
	}
	if count >= maxWebhooks {
		return nil, "", invalid("вебхуков — не больше %d", maxWebhooks)
	}
	plain := "whsec_" + strings.ToLower(secret.Token(24))
	sealed := base64.StdEncoding.EncodeToString(s.box.Seal([]byte(plain)))
	id := ids.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO notify.webhook (id, user_id, name, url, secret) VALUES ($1, $2, $3, $4, $5)`,
		id, userID, name, u, sealed); err != nil {
		return nil, "", fmt.Errorf("insert webhook: %w", err)
	}
	return &Webhook{ID: id, Name: name, URL: u, Enabled: true, CreatedAt: s.now()}, plain, nil
}

// SetWebhookEnabled включает или выключает свой вебхук.
func (s *Service) SetWebhookEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE notify.webhook SET enabled = $3 WHERE id = $1 AND user_id = $2`, id, userID, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWebhook удаляет свой вебхук с журналом доставок.
func (s *Service) DeleteWebhook(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notify.webhook WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Deliveries — журнал доставок своего вебхука, новые сверху.
func (s *Service) Deliveries(ctx context.Context, userID, id uuid.UUID, limit int) ([]Delivery, error) {
	var owner uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM notify.webhook WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && owner != userID {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id, event, status, attempts, response_code, last_error, created_at, sent_at
		FROM notify.delivery WHERE webhook_id = $1 ORDER BY created_at DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Delivery])
}

// TestWebhook отправляет пробное событие ping сразу (без очереди) и записывает доставку.
func (s *Service) TestWebhook(ctx context.Context, userID, id uuid.UUID) (*Delivery, error) {
	var owner uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM notify.webhook WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && owner != userID {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{"message": "Проверка вебхука FreightFlow"})
	if err != nil {
		return nil, err
	}
	did := ids.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO notify.delivery (id, user_id, channel, webhook_id, event, payload)
		VALUES ($1, $2, 'webhook', $3, 'ping', $4)`, did, userID, id, payload); err != nil {
		return nil, err
	}
	code, sendErr := s.sendWebhook(ctx, id, did, "ping", payload)
	status := "sent"
	var msg *string
	if sendErr != nil {
		status = "failed"
		m := strings.TrimPrefix(sendErr.Error(), errPermanent.Error()+": ")
		msg = &m
	}
	var codeArg *int
	if code > 0 {
		codeArg = &code
	}
	d := &Delivery{ID: did, Event: "ping", Status: status, Attempts: 1, ResponseCode: codeArg, LastError: msg, CreatedAt: s.now()}
	if _, err := s.pool.Exec(ctx, `UPDATE notify.delivery SET status = $2, attempts = 1, response_code = $3, last_error = $4,
		sent_at = CASE WHEN $2 = 'sent' THEN now() END WHERE id = $1`, did, status, codeArg, msg); err != nil {
		return nil, err
	}
	return d, nil
}

// VerifySignature проверяет подпись запроса вебхука на стороне получателя: подпись совпадает и
// метка времени не старше maxAge (пример для документации и тестов).
func VerifySignature(secretValue []byte, timestamp string, body []byte, signature string, now time.Time, maxAge time.Duration) bool {
	var ts int64
	if _, err := fmt.Sscan(timestamp, &ts); err != nil {
		return false
	}
	if d := now.Sub(time.Unix(ts, 0)); d > maxAge || d < -maxAge {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(Sign(secretValue, timestamp, body), "sha256="))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	return err == nil && len(got) == len(want) && hmac.Equal(got, want)
}

package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/outbound"
)

// DeliverArgs — доставка одного уведомления по внешнему каналу.
type DeliverArgs struct {
	ID uuid.UUID `json:"id"`
}

// Kind — вид задачи.
func (DeliverArgs) Kind() string { return "notify.deliver" }

// DeliverWorker доставляет уведомление. Временная ошибка — повтор с растущей паузой River;
// постоянная (адрес отвечает 4xx, бот заблокирован) или последняя попытка — статус failed.
type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — один запрос к вебхуку или почтовому серверу.
func (w *DeliverWorker) Timeout(*river.Job[DeliverArgs]) time.Duration { return time.Minute }

// errPermanent — доставка бесполезна до изменения настроек получателя.
var errPermanent = errors.New("notify: permanent delivery error")

// Work доставляет и записывает результат попытки.
func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	code, err := w.Service.deliver(ctx, job.Args.ID)
	if errors.Is(err, errNoDelivery) {
		return nil
	}
	final := err == nil || errors.Is(err, errPermanent) || job.Attempt >= job.MaxAttempts
	status := "pending"
	switch {
	case err == nil:
		status = "sent"
	case final:
		status = "failed"
	}
	var msg *string
	if err != nil {
		m := err.Error()
		if len(m) > 500 {
			m = m[:500]
		}
		msg = &m
	}
	var codeArg *int
	if code > 0 {
		codeArg = &code
	}
	if _, uerr := w.Service.pool.Exec(ctx, `UPDATE notify.delivery SET status = $2, attempts = attempts + 1,
			response_code = $3, last_error = $4, sent_at = CASE WHEN $2 = 'sent' THEN now() END WHERE id = $1`,
		job.Args.ID, status, codeArg, msg); uerr != nil {
		return uerr
	}
	if err != nil {
		w.Log.WarnContext(ctx, "notification delivery failed", "delivery", job.Args.ID, "attempt", job.Attempt, "err", err)
		if errors.Is(err, errPermanent) {
			return river.JobCancel(err)
		}
	}
	return err
}

var errNoDelivery = errors.New("notify: delivery is not pending")

type delivery struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Channel   string
	WebhookID *uuid.UUID
	Event     string
	Payload   []byte
}

// deliver выполняет одну попытку; возвращает код ответа вебхука.
func (s *Service) deliver(ctx context.Context, id uuid.UUID) (int, error) {
	var d delivery
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, channel, webhook_id, event, payload FROM notify.delivery
		WHERE id = $1 AND status = 'pending'`, id).Scan(&d.ID, &d.UserID, &d.Channel, &d.WebhookID, &d.Event, &d.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNoDelivery
	}
	if err != nil {
		return 0, err
	}
	var p alertPayload
	if err := json.Unmarshal(d.Payload, &p); err != nil {
		return 0, fmt.Errorf("%w: payload: %w", errPermanent, err)
	}
	switch d.Channel {
	case "email":
		return 0, s.sendEmail(ctx, d.UserID, p)
	case "telegram":
		return 0, s.sendTelegram(ctx, d.UserID, p)
	case "webhook":
		return s.sendWebhook(ctx, *d.WebhookID, d.ID, d.Event, d.Payload)
	}
	return 0, fmt.Errorf("%w: unknown channel %s", errPermanent, d.Channel)
}

func (s *Service) item(p alertPayload) mailItem {
	return mailItem{
		Severity: p.Alert.Severity, Title: p.Alert.Title, Summary: p.Alert.Summary, Rule: p.Alert.RuleName,
		When: p.Alert.CreatedAt.In(moscow).Format("02.01.2006 15:04"), Link: p.Link,
	}
}

func (s *Service) userEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	var email, status string
	if err := s.pool.QueryRow(ctx, `SELECT email, status FROM core.app_user WHERE id = $1`, userID).Scan(&email, &status); err != nil {
		return "", fmt.Errorf("%w: user: %w", errPermanent, err)
	}
	if status != "active" {
		return "", fmt.Errorf("%w: user is %s", errPermanent, status)
	}
	return email, nil
}

func render(ctx context.Context, c templ.Component) (string, error) {
	var b bytes.Buffer
	if err := c.Render(ctx, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (s *Service) sendEmail(ctx context.Context, userID uuid.UUID, p alertPayload) error {
	to, err := s.userEmail(ctx, userID)
	if err != nil {
		return err
	}
	it := s.item(p)
	heading := ""
	subject := "[" + severityLabels[it.Severity] + "] " + it.Title
	if p.Event == "alert.assigned" {
		heading = "Вам назначен алерт."
		subject = "Вам назначен алерт: " + it.Title
	}
	body, err := render(ctx, alertEmail(it, heading)) //nolint:contextcheck // контекст передаётся в Render, шаблон его не создаёт
	if err != nil {
		return err
	}
	text := strings.TrimSpace(heading + "\n\n" + severityLabels[it.Severity] + ": " + it.Title + "\n" + it.Summary +
		"\nПравило «" + it.Rule + "», " + it.When + "\n\n" + it.Link + "\n")
	return s.mailer.Send(ctx, mail.Message{To: to, Subject: subject, Text: text, HTML: body})
}

func (s *Service) sendTelegram(ctx context.Context, userID uuid.UUID, p alertPayload) error {
	if s.telegram == nil {
		return fmt.Errorf("%w: telegram is not configured", errPermanent)
	}
	var chat int64
	if err := s.pool.QueryRow(ctx, `SELECT chat_id FROM notify.telegram_binding WHERE user_id = $1`, userID).Scan(&chat); err != nil {
		return fmt.Errorf("%w: telegram binding: %w", errPermanent, err)
	}
	it := s.item(p)
	var b strings.Builder
	if p.Event == "alert.assigned" {
		b.WriteString("Вам назначен алерт\n")
	}
	fmt.Fprintf(&b, "<b>%s</b> · %s\n", severityLabels[it.Severity], html.EscapeString(it.Title))
	if it.Summary != "" {
		b.WriteString(html.EscapeString(it.Summary) + "\n")
	}
	fmt.Fprintf(&b, "<i>Правило «%s», %s</i>\n<a href=\"%s\">Открыть алерт</a>", html.EscapeString(it.Rule), it.When, html.EscapeString(it.Link))
	err := s.telegram.send(ctx, chat, b.String())
	if errors.Is(err, errBotBlocked) {
		// Пользователь заблокировал бота — привязка больше не действует.
		if _, derr := s.pool.Exec(ctx, `DELETE FROM notify.telegram_binding WHERE user_id = $1`, userID); derr != nil {
			return derr
		}
		return fmt.Errorf("%w: %w", errPermanent, err)
	}
	return err
}

// Sign — подпись вебхука: HMAC-SHA256 от «метка времени.тело» в hex. Получатель проверяет её
// и отбрасывает запросы со старой меткой (защита от повтора).
func Sign(secret []byte, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(timestamp))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// webhookBody — тело запроса вебхука.
type webhookBody struct {
	ID     uuid.UUID       `json:"id"`
	Event  string          `json:"event"`
	SentAt time.Time       `json:"sentAt"`
	Data   json.RawMessage `json:"data"`
}

func (s *Service) sendWebhook(ctx context.Context, hookID, deliveryID uuid.UUID, event string, payload []byte) (int, error) {
	var url, sealed string
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT url, secret, enabled FROM notify.webhook WHERE id = $1`, hookID).Scan(&url, &sealed, &enabled)
	if err != nil {
		return 0, fmt.Errorf("%w: webhook: %w", errPermanent, err)
	}
	if !enabled {
		return 0, fmt.Errorf("%w: webhook is disabled", errPermanent)
	}
	secret, err := s.openSecret(sealed)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errPermanent, err)
	}
	body, err := json.Marshal(webhookBody{ID: deliveryID, Event: event, SentAt: s.now().UTC(), Data: payload})
	if err != nil {
		return 0, err
	}
	ts := strconv.FormatInt(s.now().Unix(), 10)
	h := http.Header{}
	h.Set("X-FreightFlow-Event", event)
	h.Set("X-FreightFlow-Delivery", deliveryID.String())
	h.Set("X-FreightFlow-Timestamp", ts)
	h.Set("X-FreightFlow-Signature", Sign(secret, ts, body))
	resp, err := s.webhooks.PostHeaders(ctx, url, "application/json", body, h)
	if err != nil {
		code := outbound.StatusCode(err)
		// 4xx, кроме 408 и 429, — адрес не примет и повтор: настройки получателя неверны.
		if code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests ||
			errors.Is(err, outbound.ErrPrivateAddress) || errors.Is(err, outbound.ErrHostNotAllowed) {
			return code, fmt.Errorf("%w: %w", errPermanent, err)
		}
		return code, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (s *Service) openSecret(sealed string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	return s.box.Open(raw)
}

// DigestArgs — ежечасная отправка дайджестов тем, у кого наступил их час.
type DigestArgs struct{}

// Kind — вид задачи.
func (DigestArgs) Kind() string { return "notify.digest" }

// DigestWorker отправляет дайджесты.
type DigestWorker struct {
	river.WorkerDefaults[DigestArgs]
	Service *Service
	Log     *slog.Logger
}

// Work собирает отложенные письма пользователей, у которых сейчас час дайджеста по Москве.
func (w *DigestWorker) Work(ctx context.Context, _ *river.Job[DigestArgs]) error {
	n, err := w.Service.SendDigests(ctx)
	if n > 0 {
		w.Log.InfoContext(ctx, "digests sent", "users", n)
	}
	return err
}

// SendDigests отправляет дайджест каждому пользователю, у которого наступил час дайджеста;
// возвращает число отправленных писем.
func (s *Service) SendDigests(ctx context.Context) (int, error) {
	hour := s.now().In(moscow).Hour()
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT d.user_id FROM notify.delivery d
		LEFT JOIN notify.preference p ON p.user_id = d.user_id
		WHERE d.status = 'digest' AND coalesce(p.digest_hour, 9) = $1`, hour)
	if err != nil {
		return 0, fmt.Errorf("digest users: %w", err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, u := range users {
		if err := s.digest(ctx, u); err != nil {
			s.log.WarnContext(ctx, "digest failed", "user", u, "err", err)
			continue
		}
		sent++
	}
	return sent, nil
}

func (s *Service) digest(ctx context.Context, userID uuid.UUID) error {
	to, err := s.userEmail(ctx, userID)
	if err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, payload FROM notify.delivery WHERE user_id = $1 AND status = 'digest'
		ORDER BY created_at LIMIT 200`, userID)
	if err != nil {
		return err
	}
	var idsSent []uuid.UUID
	var items []mailItem
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var p alertPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		idsSent = append(idsSent, id)
		items = append(items, s.item(p))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	body, err := render(ctx, digestEmail(items, s.cfg.BaseURL+"/alerts")) //nolint:contextcheck // контекст передаётся в Render
	if err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Новые алерты по вашим правилам: %d.\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&text, "\n%s: %s\n%s\n%s\n", severityLabels[it.Severity], it.Title, it.Summary, it.Link)
	}
	if err := s.mailer.Send(ctx, mail.Message{
		To: to, Subject: fmt.Sprintf("FreightFlow: алерты за сутки (%d)", len(items)),
		Text: text.String(), HTML: body,
	}); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE notify.delivery SET status = 'sent', sent_at = now(), attempts = attempts + 1
		WHERE id = ANY($1)`, idsSent)
	return err
}

// Register добавляет доставку, дайджесты и опрос бота.
func Register(reg *jobs.Registry, s *Service, log *slog.Logger) {
	jobs.Register(reg, &DeliverWorker{Service: s, Log: log})
	jobs.Register(reg, &DigestWorker{Service: s, Log: log})
	reg.Every(DigestArgs{}.Kind(), time.Hour, DigestArgs{}, &river.InsertOpts{Queue: jobs.QueueNotify}, false)
}

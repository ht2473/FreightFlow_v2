package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"freightflow/internal/alerting"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/jobs"
)

// alertEvent — данные событий alert.created и alert.updated, нужные рассылке.
type alertEvent struct {
	AlertID    uuid.UUID  `json:"alertId"`
	Action     string     `json:"action"`
	AssigneeID *uuid.UUID `json:"assigneeId"`
}

// recipient — кому и по каким каналам отправить уведомление.
type recipient struct {
	UserID   uuid.UUID
	Channels []string // in_app, email, telegram, webhook
	Kind     string   // alert_created, alert_assigned
}

// Consumer — постоянный потребитель событий алертов: уведомления, доставки и задачи создаются
// в транзакции, сдвигающей позицию, поэтому ни один алерт не остаётся без уведомления и ни одно
// не отправляется дважды.
func (s *Service) Consumer() events.Consumer {
	return events.Consumer{
		Name:  "notify.alerts",
		Types: []string{events.AlertCreated, events.AlertUpdated},
		Handle: func(ctx context.Context, tx pgx.Tx, evs []events.Event) error {
			for _, e := range evs {
				var ae alertEvent
				if err := json.Unmarshal(e.Payload, &ae); err != nil {
					return fmt.Errorf("alert event %d: %w", e.ID, err)
				}
				if err := s.dispatch(ctx, tx, ae); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// dispatch рассылает новый алерт владельцу правила и подписанным операторам, назначенный —
// ответственному. Повторы и смена статуса уведомлений не порождают.
func (s *Service) dispatch(ctx context.Context, tx pgx.Tx, ae alertEvent) error {
	var rcpt []recipient
	switch {
	case ae.Action == "created":
	case ae.Action == "assigned" && ae.AssigneeID != nil:
	default:
		return nil
	}
	n, err := s.alerts.Notice(ctx, ae.AlertID)
	if err != nil {
		if errors.Is(err, alerting.ErrNotFound) {
			return nil // правило удалено раньше, чем дошло событие
		}
		return err
	}
	a := &n.Alert
	if ae.Action == "created" {
		rcpt = append(rcpt, recipient{UserID: a.OwnerID, Channels: n.Channels, Kind: "alert_created"})
		rows, err := tx.Query(ctx, `
			SELECT p.user_id FROM notify.preference p JOIN core.app_user u ON u.id = p.user_id
			WHERE p.subscribe_all AND u.status = 'active' AND p.user_id <> $1
			  AND array_position(ARRAY['info','warning','critical'], $2) >= array_position(ARRAY['info','warning','critical'], p.subscribe_severity)
			  AND EXISTS (SELECT 1 FROM core.user_role ur JOIN core.role_permission rp ON rp.role_code = ur.role_code
				WHERE ur.user_id = p.user_id AND rp.permission = 'alerts.manage')`, a.OwnerID, a.Severity)
		if err != nil {
			return fmt.Errorf("subscribers: %w", err)
		}
		subs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range subs {
			rcpt = append(rcpt, recipient{UserID: id, Channels: []string{"in_app", "email", "telegram"}, Kind: "alert_created"})
		}
	} else {
		rcpt = append(rcpt, recipient{UserID: *ae.AssigneeID, Channels: []string{"in_app", "email", "telegram"}, Kind: "alert_assigned"})
	}
	for _, r := range rcpt {
		if err := s.notify(ctx, tx, r, a); err != nil {
			return err
		}
	}
	return nil
}

// alertPayload — данные алерта в доставке: тело вебхука и текст сообщений.
type alertPayload struct {
	Event string         `json:"event"`
	Link  string         `json:"link"`
	Alert alerting.Alert `json:"alert"`
}

func (s *Service) alertLink(id uuid.UUID) string {
	return s.cfg.BaseURL + "/alerts?alert=" + id.String()
}

// notify создаёт уведомление в системе и доставки по каналам получателя с учётом его
// предпочтений: порог важности, режим почты, тихие часы (кроме критичных и вебхуков).
func (s *Service) notify(ctx context.Context, tx pgx.Tx, r recipient, a *alerting.Alert) error {
	pref, err := preferences(ctx, tx, r.UserID)
	if err != nil {
		return err
	}
	title := a.Title
	if r.Kind == "alert_assigned" {
		title = "Вам назначен алерт: " + a.Title
	}
	link := s.alertLink(a.ID)
	var notificationID *uuid.UUID
	if slices.Contains(r.Channels, "in_app") {
		id := ids.New()
		notificationID = &id
		path := "/alerts?alert=" + a.ID.String()
		if _, err := tx.Exec(ctx, `INSERT INTO notify.notification (id, user_id, kind, severity, title, body, link, alert_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, r.UserID, r.Kind, a.Severity, title, a.Summary, path, a.ID); err != nil {
			return fmt.Errorf("insert notification: %w", err)
		}
		if err := events.Publish(ctx, tx, events.NotificationCreated, map[string]any{
			"userId": r.UserID, "notificationId": id, "severity": a.Severity, "title": title,
		}); err != nil {
			return err
		}
	}
	event := "alert.created"
	if r.Kind == "alert_assigned" {
		event = "alert.assigned"
	}
	payload, err := json.Marshal(alertPayload{Event: event, Link: link, Alert: *a})
	if err != nil {
		return err
	}
	now := s.now()
	quiet := time.Time{}
	if a.Severity != "critical" {
		quiet = pref.quietUntil(now)
	}
	important := severityAtLeast(a.Severity, pref.MinSeverity)
	add := func(channel string, webhook *uuid.UUID, status string, at time.Time) error {
		id := ids.New()
		if at.IsZero() {
			at = now
		}
		if _, err := tx.Exec(ctx, `INSERT INTO notify.delivery (id, notification_id, user_id, channel, webhook_id, event,
				payload, status, scheduled_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, notificationID, r.UserID, channel, webhook, event, payload, status, at); err != nil {
			return fmt.Errorf("insert delivery: %w", err)
		}
		if status != "pending" || s.jobs == nil {
			return nil
		}
		_, err := s.jobs.InsertTx(ctx, tx, DeliverArgs{ID: id}, deliverOpts(at))
		return err
	}
	if slices.Contains(r.Channels, "email") && important && pref.EmailMode != "off" {
		status := "pending"
		if pref.EmailMode == "digest" && a.Severity != "critical" {
			status = "digest"
		}
		if err := add("email", nil, status, quiet); err != nil {
			return err
		}
	}
	if slices.Contains(r.Channels, "telegram") && important && s.telegram != nil {
		var bound bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notify.telegram_binding WHERE user_id = $1)`, r.UserID).Scan(&bound); err != nil {
			return err
		}
		if bound {
			if err := add("telegram", nil, "pending", quiet); err != nil {
				return err
			}
		}
	}
	if slices.Contains(r.Channels, "webhook") {
		rows, err := tx.Query(ctx, `SELECT id FROM notify.webhook WHERE user_id = $1 AND enabled ORDER BY created_at`, r.UserID)
		if err != nil {
			return err
		}
		hooks, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, h := range hooks {
			if err := add("webhook", &h, "pending", time.Time{}); err != nil {
				return err
			}
		}
	}
	return nil
}

// deliverAttempts — попыток доставки: паузы River растут до нескольких часов.
const deliverAttempts = 8

func deliverOpts(at time.Time) *river.InsertOpts {
	return &river.InsertOpts{Queue: jobs.QueueNotify, MaxAttempts: deliverAttempts, ScheduledAt: at}
}

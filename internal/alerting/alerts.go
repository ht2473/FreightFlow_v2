package alerting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
)

// Filter — отбор ленты алертов.
type Filter struct {
	Status   []string
	Severity []string
	Kind     string
	RuleID   *uuid.UUID
	Assigned bool // только назначенные мне
	Query    string
	Limit    int
	Cursor   string
}

// Page — страница ленты.
type Page struct {
	Items []Alert
	Next  string
}

const alertColumns = `a.id, a.rule_id, r.name, r.kind, r.owner_id, a.entity, a.severity, a.status, a.title, a.summary,
	a.details, ST_AsGeoJSON(a.geom, 6), a.occurrences, a.assignee_id, asg.name, a.acknowledged_at, a.closed_at,
	a.resolution, a.created_at, a.updated_at, a.last_seen_at`

const alertFrom = ` FROM alerting.alert a JOIN alerting.rule r ON r.id = a.rule_id
	LEFT JOIN core.app_user asg ON asg.id = a.assignee_id`

// visibleTo — алерт виден владельцу правила, назначенному и оператору (параметры $1, $2).
const visibleTo = `($2 OR r.owner_id = $1 OR a.assignee_id = $1)`

func scanAlert(row pgx.Row) (*Alert, error) {
	var a Alert
	var details []byte
	var geom *string
	err := row.Scan(&a.ID, &a.RuleID, &a.RuleName, &a.Kind, &a.OwnerID, &a.Entity, &a.Severity, &a.Status, &a.Title,
		&a.Summary, &details, &geom, &a.Occurrences, &a.AssigneeID, &a.AssigneeName, &a.AcknowledgedAt, &a.ClosedAt,
		&a.Resolution, &a.CreatedAt, &a.UpdatedAt, &a.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Details = details
	if geom != nil {
		a.Geometry = json.RawMessage(*geom)
	}
	return &a, nil
}

type cursor struct {
	At time.Time `json:"t"`
	ID uuid.UUID `json:"i"`
}

func encodeCursor(a *Alert) string {
	b, _ := json.Marshal(cursor{At: a.CreatedAt, ID: a.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (*cursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, invalid("неверный курсор")
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, invalid("неверный курсор")
	}
	return &c, nil
}

// List — лента алертов, новые сверху.
func (s *Service) List(ctx context.Context, a Actor, f Filter) (*Page, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	cur, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, err
	}
	where := []string{visibleTo}
	args := []any{a.UserID, a.Manage}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if len(f.Status) > 0 {
		add("a.status = ANY(?)", f.Status)
	}
	if len(f.Severity) > 0 {
		add("a.severity = ANY(?)", f.Severity)
	}
	if f.Kind != "" {
		add("r.kind = ?", f.Kind)
	}
	if f.RuleID != nil {
		add("a.rule_id = ?", *f.RuleID)
	}
	if f.Assigned {
		where = append(where, "a.assignee_id = $1")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("(a.title ILIKE '%' || ? || '%' OR a.summary ILIKE '%' || ? || '%')", q)
	}
	if cur != nil {
		args = append(args, cur.At, cur.ID)
		where = append(where, fmt.Sprintf("(a.created_at, a.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)
	rows, err := s.pool.Query(ctx, `SELECT `+alertColumns+alertFrom+` WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` ORDER BY a.created_at DESC, a.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	page := &Page{}
	for rows.Next() {
		al, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, *al)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		page.Next = encodeCursor(&page.Items[f.Limit-1])
	}
	return page, nil
}

// Summary — число открытых алертов по важности.
type Summary struct {
	Active       int            `json:"active"`
	Acknowledged int            `json:"acknowledged"`
	BySeverity   map[string]int `json:"bySeverity"` // активные
}

// Summary считает открытые алерты, видимые пользователю.
func (s *Service) Summary(ctx context.Context, a Actor) (*Summary, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.status, a.severity, count(*)`+alertFrom+
		` WHERE a.status <> 'closed' AND `+visibleTo+` GROUP BY 1, 2`, a.UserID, a.Manage)
	if err != nil {
		return nil, fmt.Errorf("alert summary: %w", err)
	}
	defer rows.Close()
	out := &Summary{BySeverity: map[string]int{}}
	for rows.Next() {
		var status, sev string
		var n int
		if err := rows.Scan(&status, &sev, &n); err != nil {
			return nil, err
		}
		if status == StatusActive {
			out.Active += n
			out.BySeverity[sev] += n
		} else {
			out.Acknowledged += n
		}
	}
	return out, rows.Err()
}

// Get — алерт.
func (s *Service) Get(ctx context.Context, a Actor, id uuid.UUID) (*Alert, error) {
	return scanAlert(s.pool.QueryRow(ctx, `SELECT `+alertColumns+alertFrom+` WHERE a.id = $3 AND `+visibleTo, a.UserID, a.Manage, id))
}

// Log — журнал алерта по порядку.
func (s *Service) Log(ctx context.Context, a Actor, id uuid.UUID) ([]LogEntry, error) {
	if _, err := s.Get(ctx, a, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.at, l.actor_id, u.name, l.action, l.comment, l.details
		FROM alerting.alert_log l LEFT JOIN core.app_user u ON u.id = l.actor_id
		WHERE l.alert_id = $1 ORDER BY l.id`, id)
	if err != nil {
		return nil, fmt.Errorf("alert log: %w", err)
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorName, &e.Action, &e.Comment, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Действия с алертом.
const (
	ActionAcknowledge = "acknowledge"
	ActionClose       = "close"
	ActionReopen      = "reopen"
	ActionAssign      = "assign"
	ActionComment     = "comment"
)

// Change — действие пользователя: смена статуса, назначение или комментарий (к любому действию
// можно приложить комментарий).
type Change struct {
	Action   string
	Assignee *uuid.UUID // для assign; nil — снять назначение
	Comment  string
}

// Act выполняет действие. Статус и назначение меняют владелец правила и оператор,
// комментировать может каждый, кому алерт виден.
func (s *Service) Act(ctx context.Context, a Actor, id uuid.UUID, c Change) (*Alert, error) {
	comment := strings.TrimSpace(c.Comment)
	if len([]rune(comment)) > 4000 {
		return nil, invalid("комментарий длиннее 4000 символов")
	}
	if c.Action == ActionComment && comment == "" {
		return nil, invalid("пустой комментарий")
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var owner uuid.UUID
		var status, kind, severity string
		var assignee *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT r.owner_id, a.status, r.kind, a.severity, a.assignee_id
			FROM alerting.alert a JOIN alerting.rule r ON r.id = a.rule_id
			WHERE a.id = $1 FOR UPDATE OF a`, id).Scan(&owner, &status, &kind, &severity, &assignee)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		canSee := a.Manage || owner == a.UserID || assignee != nil && *assignee == a.UserID
		if !canSee {
			return ErrNotFound
		}
		canManage := a.Manage || owner == a.UserID
		var set, logAction string
		var setArgs []any
		var details map[string]any
		switch c.Action {
		case ActionAcknowledge:
			if status != StatusActive {
				return invalid("подтвердить можно только активный алерт")
			}
			set, setArgs = `status = 'acknowledged', acknowledged_by = $2, acknowledged_at = now()`, []any{a.UserID}
			logAction = "acknowledged"
		case ActionClose:
			if status == StatusClosed {
				return invalid("алерт уже закрыт")
			}
			set, setArgs = `status = 'closed', closed_by = $2, closed_at = now(), resolution = 'manual'`, []any{a.UserID}
			logAction = "closed"
		case ActionReopen:
			if status != StatusClosed {
				return invalid("открыть заново можно только закрытый алерт")
			}
			// Открытым может быть один алерт правила по сущности.
			var busy bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alerting.alert o, alerting.alert a
				WHERE a.id = $1 AND o.rule_id = a.rule_id AND o.entity = a.entity AND o.status <> 'closed')`, id).Scan(&busy); err != nil {
				return err
			}
			if busy {
				return invalid("по этому объекту уже есть открытый алерт")
			}
			set = `status = 'active', closed_by = NULL, closed_at = NULL, resolution = NULL,
				acknowledged_by = NULL, acknowledged_at = NULL`
			logAction = "reopened"
		case ActionAssign:
			if c.Assignee != nil {
				var ok bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.app_user WHERE id = $1 AND status = 'active')`,
					*c.Assignee).Scan(&ok); err != nil {
					return err
				}
				if !ok {
					return invalid("пользователь для назначения не найден")
				}
			}
			set, setArgs = `assignee_id = $2`, []any{c.Assignee}
			logAction = "assigned"
			details = map[string]any{"assigneeId": c.Assignee}
		case ActionComment:
			logAction = "commented"
		default:
			return invalid("неизвестное действие %q", c.Action)
		}
		if set != "" && !canManage {
			return ErrForbidden
		}
		if set != "" {
			if _, err := tx.Exec(ctx, `UPDATE alerting.alert SET `+set+`, updated_at = now() WHERE id = $1`,
				append([]any{id}, setArgs...)...); err != nil {
				return fmt.Errorf("update alert: %w", err)
			}
		}
		var commentArg *string
		if comment != "" {
			commentArg = &comment
		}
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return err
		}
		if details == nil {
			detailsJSON = []byte(`{}`)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO alerting.alert_log (alert_id, actor_id, action, comment, details)
			VALUES ($1, $2, $3, $4, $5)`, id, a.UserID, logAction, commentArg, detailsJSON); err != nil {
			return err
		}
		payload := map[string]any{"alertId": id, "ownerId": owner, "kind": kind, "severity": severity, "action": logAction}
		if c.Assignee != nil {
			payload["assigneeId"] = *c.Assignee
		}
		return events.Publish(ctx, tx, events.AlertUpdated, payload)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, a, id)
}

// Notice — алерт для рассылки: каналы правила и его владелец.
type Notice struct {
	Alert    Alert
	Channels []string
}

// Notice возвращает алерт для рассылки уведомлений без проверки видимости (вызывает доставщик).
func (s *Service) Notice(ctx context.Context, id uuid.UUID) (*Notice, error) {
	var n Notice
	row := s.pool.QueryRow(ctx, `SELECT `+alertColumns+`, r.channels`+alertFrom+` WHERE a.id = $1`, id)
	var details []byte
	var geom *string
	a := &n.Alert
	err := row.Scan(&a.ID, &a.RuleID, &a.RuleName, &a.Kind, &a.OwnerID, &a.Entity, &a.Severity, &a.Status, &a.Title,
		&a.Summary, &details, &geom, &a.Occurrences, &a.AssigneeID, &a.AssigneeName, &a.AcknowledgedAt, &a.ClosedAt,
		&a.Resolution, &a.CreatedAt, &a.UpdatedAt, &a.LastSeenAt, &n.Channels)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Details = details
	if geom != nil {
		a.Geometry = json.RawMessage(*geom)
	}
	return &n, nil
}

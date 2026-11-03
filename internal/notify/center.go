package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Page — страница центра уведомлений.
type Page struct {
	Items  []Notification
	Next   string
	Unread int
}

type cursor struct {
	At time.Time `json:"t"`
	ID uuid.UUID `json:"i"`
}

// List — уведомления пользователя, новые сверху, и число непрочитанных.
func (s *Service) List(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int, after string) (*Page, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var cur *cursor
	if after != "" {
		b, err := base64.RawURLEncoding.DecodeString(after)
		if err != nil {
			return nil, invalid("неверный курсор")
		}
		cur = &cursor{}
		if err := json.Unmarshal(b, cur); err != nil {
			return nil, invalid("неверный курсор")
		}
	}
	args := []any{userID, unreadOnly, limit + 1}
	where := ""
	if cur != nil {
		args = append(args, cur.At, cur.ID)
		where = ` AND (created_at, id) < ($4, $5)`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, severity, title, body, link, alert_id, read_at, created_at FROM notify.notification
		WHERE user_id = $1 AND (NOT $2 OR read_at IS NULL)`+where+`
		ORDER BY created_at DESC, id DESC LIMIT $3`, args...)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Notification])
	if err != nil {
		return nil, err
	}
	page := &Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		b, _ := json.Marshal(cursor{At: last.CreatedAt, ID: last.ID})
		page.Next = base64.RawURLEncoding.EncodeToString(b)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notify.notification WHERE user_id = $1 AND read_at IS NULL`,
		userID).Scan(&page.Unread); err != nil {
		return nil, err
	}
	return page, nil
}

// MarkRead отмечает прочитанными уведомления ids или все (ids пустой и all).
func (s *Service) MarkRead(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, all bool) error {
	if !all && len(ids) == 0 {
		return invalid("не указано, какие уведомления отметить")
	}
	_, err := s.pool.Exec(ctx, `UPDATE notify.notification SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL AND ($2 OR id = ANY($3))`, userID, all, ids)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// Preferences — предпочтения доставки пользователя.
type Preferences struct {
	EmailMode         string `json:"emailMode"` // instant, digest, off
	DigestHour        int    `json:"digestHour"`
	QuietFrom         *int   `json:"quietFrom,omitempty"`
	QuietTo           *int   `json:"quietTo,omitempty"`
	MinSeverity       string `json:"minSeverity"` // ниже — только в системе
	SubscribeAll      bool   `json:"subscribeAll"`
	SubscribeSeverity string `json:"subscribeSeverity"`
}

// defaults — предпочтения пользователя, ещё не менявшего их.
func defaults() Preferences {
	return Preferences{EmailMode: "instant", DigestHour: 9, MinSeverity: "info", SubscribeSeverity: "critical"}
}

var severities = []string{"info", "warning", "critical"}

func severityAtLeast(sev, min string) bool {
	return slices.Index(severities, sev) >= slices.Index(severities, min)
}

// Validate проверяет предпочтения.
func (p *Preferences) Validate() error {
	if !slices.Contains([]string{"instant", "digest", "off"}, p.EmailMode) {
		return invalid("неизвестный режим почты %q", p.EmailMode)
	}
	if p.DigestHour < 0 || p.DigestHour > 23 {
		return invalid("час дайджеста — от 0 до 23")
	}
	if (p.QuietFrom == nil) != (p.QuietTo == nil) {
		return invalid("тихие часы задаются началом и концом")
	}
	if p.QuietFrom != nil {
		if *p.QuietFrom < 0 || *p.QuietFrom > 23 || *p.QuietTo < 0 || *p.QuietTo > 23 {
			return invalid("тихие часы — от 0 до 23")
		}
		if *p.QuietFrom == *p.QuietTo {
			return invalid("начало и конец тихих часов совпадают")
		}
	}
	if !slices.Contains(severities, p.MinSeverity) || !slices.Contains(severities, p.SubscribeSeverity) {
		return invalid("неизвестная важность")
	}
	return nil
}

// Preferences — предпочтения пользователя (по умолчанию, если не заданы).
func (s *Service) Preferences(ctx context.Context, userID uuid.UUID) (Preferences, error) {
	return preferences(ctx, s.pool, userID)
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func preferences(ctx context.Context, q rowQuerier, userID uuid.UUID) (Preferences, error) {
	p := defaults()
	var from, to *int16
	var hour int16
	err := q.QueryRow(ctx, `SELECT email_mode, digest_hour, quiet_from, quiet_to, min_severity, subscribe_all, subscribe_severity
		FROM notify.preference WHERE user_id = $1`, userID).
		Scan(&p.EmailMode, &hour, &from, &to, &p.MinSeverity, &p.SubscribeAll, &p.SubscribeSeverity)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("preferences: %w", err)
	}
	p.DigestHour = int(hour)
	if from != nil && to != nil {
		f, t := int(*from), int(*to)
		p.QuietFrom, p.QuietTo = &f, &t
	}
	return p, nil
}

// SetPreferences сохраняет предпочтения. Подписка на все алерты — только оператору (manage).
func (s *Service) SetPreferences(ctx context.Context, userID uuid.UUID, manage bool, p Preferences) (Preferences, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	if !manage {
		p.SubscribeAll = false
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notify.preference (user_id, email_mode, digest_hour, quiet_from, quiet_to, min_severity,
			subscribe_all, subscribe_severity)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET email_mode = $2, digest_hour = $3, quiet_from = $4, quiet_to = $5,
			min_severity = $6, subscribe_all = $7, subscribe_severity = $8, updated_at = now()`,
		userID, p.EmailMode, p.DigestHour, p.QuietFrom, p.QuietTo, p.MinSeverity, p.SubscribeAll, p.SubscribeSeverity)
	if err != nil {
		return p, fmt.Errorf("save preferences: %w", err)
	}
	return p, nil
}

// quietUntil — конец тихих часов, если now в них попадает; иначе нулевое время. Интервал
// [from, to) по часам Москвы, через полночь — если from > to.
func (p Preferences) quietUntil(now time.Time) time.Time {
	if p.QuietFrom == nil || p.QuietTo == nil {
		return time.Time{}
	}
	local := now.In(moscow)
	h, from, to := local.Hour(), *p.QuietFrom, *p.QuietTo
	inside := from < to && h >= from && h < to || from > to && (h >= from || h < to)
	if !inside {
		return time.Time{}
	}
	end := time.Date(local.Year(), local.Month(), local.Day(), to, 0, 0, 0, moscow)
	if !end.After(local) {
		end = end.AddDate(0, 0, 1)
	}
	return end
}

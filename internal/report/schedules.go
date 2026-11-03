package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
)

// moscow — часовой пояс расписаний.
var moscow = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}()

// ScheduleInput — расписание отчёта, как его задаёт пользователь.
type ScheduleInput struct {
	Template   string
	Format     string
	Params     map[string]string
	Title      string
	Frequency  string // daily, weekly, monthly
	Weekday    *int   // 1 — понедельник (weekly)
	MonthDay   *int   // 1–28 (monthly)
	Hour       int    // по Москве
	Recipients []string
	Enabled    bool
}

// Schedule — сохранённое расписание.
type Schedule struct {
	ScheduleInput
	ID        uuid.UUID
	NextRunAt time.Time
	LastRunAt *time.Time
	CreatedAt time.Time
}

// maxRecipients — адресов в рассылке.
const maxRecipients = 10

// Validate проверяет расписание и приводит его к каноническому виду.
func (in *ScheduleInput) Validate() error {
	t, ok := TemplateByCode(in.Template)
	if !ok {
		return invalid("неизвестный шаблон отчёта %q", in.Template)
	}
	if in.Format != FormatPDF && in.Format != FormatXLSX {
		return invalid("формат отчёта — pdf или xlsx")
	}
	params, err := t.ValidateParams(in.Params)
	if err != nil {
		return err
	}
	in.Params = params
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		in.Title = t.Title
	}
	if n := len([]rune(in.Title)); n > 200 {
		return invalid("название — до 200 символов")
	}
	switch in.Frequency {
	case "daily":
		in.Weekday, in.MonthDay = nil, nil
	case "weekly":
		if in.Weekday == nil || *in.Weekday < 1 || *in.Weekday > 7 {
			return invalid("день недели — от 1 (понедельник) до 7")
		}
		in.MonthDay = nil
	case "monthly":
		if in.MonthDay == nil || *in.MonthDay < 1 || *in.MonthDay > 28 {
			return invalid("день месяца — от 1 до 28")
		}
		in.Weekday = nil
	default:
		return invalid("периодичность — daily, weekly или monthly")
	}
	if in.Hour < 0 || in.Hour > 23 {
		return invalid("час — от 0 до 23")
	}
	if len(in.Recipients) > maxRecipients {
		return invalid("получателей — не больше %d", maxRecipients)
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, r := range in.Recipients {
		a, err := mail.ParseAddress(strings.TrimSpace(r))
		if err != nil || !strings.Contains(a.Address, ".") {
			return invalid("неверный адрес почты %q", r)
		}
		addr := strings.ToLower(a.Address)
		if !seen[addr] {
			seen[addr] = true
			clean = append(clean, addr)
		}
	}
	in.Recipients = clean
	return nil
}

// NextRun — ближайший момент запуска после now по Москве.
func (in ScheduleInput) NextRun(now time.Time) time.Time {
	local := now.In(moscow)
	at := time.Date(local.Year(), local.Month(), local.Day(), in.Hour, 0, 0, 0, moscow)
	switch in.Frequency {
	case "weekly":
		wd := int(at.Weekday())
		if wd == 0 {
			wd = 7
		}
		at = at.AddDate(0, 0, (*in.Weekday-wd+7)%7)
		if !at.After(local) {
			at = at.AddDate(0, 0, 7)
		}
	case "monthly":
		at = time.Date(local.Year(), local.Month(), *in.MonthDay, in.Hour, 0, 0, 0, moscow)
		if !at.After(local) {
			at = at.AddDate(0, 1, 0)
		}
	default:
		if !at.After(local) {
			at = at.AddDate(0, 0, 1)
		}
	}
	return at
}

const scheduleColumns = `id, template, format, params, title, frequency, weekday, month_day, hour, recipients, enabled,
	next_run_at, last_run_at, created_at`

func scanSchedule(r pgx.Row) (*Schedule, error) {
	var sc Schedule
	var params []byte
	var wd, md *int16
	var hour int16
	err := r.Scan(&sc.ID, &sc.Template, &sc.Format, &params, &sc.Title, &sc.Frequency, &wd, &md, &hour, &sc.Recipients,
		&sc.Enabled, &sc.NextRunAt, &sc.LastRunAt, &sc.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sc.Hour = int(hour)
	if wd != nil {
		v := int(*wd)
		sc.Weekday = &v
	}
	if md != nil {
		v := int(*md)
		sc.MonthDay = &v
	}
	return &sc, json.Unmarshal(params, &sc.Params)
}

// Schedules — расписания пользователя.
func (s *Service) Schedules(ctx context.Context, userID uuid.UUID) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+scheduleColumns+` FROM report.schedule WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Schedule, error) {
		sc, err := scanSchedule(r)
		if err != nil {
			return Schedule{}, err
		}
		return *sc, nil
	})
}

// SaveSchedule создаёт (id == nil) или меняет своё расписание.
func (s *Service) SaveSchedule(ctx context.Context, userID uuid.UUID, id *uuid.UUID, in ScheduleInput) (*Schedule, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	params, err := json.Marshal(in.Params)
	if err != nil {
		return nil, err
	}
	next := in.NextRun(time.Now())
	if id == nil {
		return scanSchedule(s.pool.QueryRow(ctx, `
			INSERT INTO report.schedule (id, user_id, template, format, params, title, frequency, weekday, month_day, hour,
				recipients, enabled, next_run_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING `+scheduleColumns,
			ids.New(), userID, in.Template, in.Format, params, in.Title, in.Frequency, in.Weekday, in.MonthDay, in.Hour,
			in.Recipients, in.Enabled, next))
	}
	return scanSchedule(s.pool.QueryRow(ctx, `
		UPDATE report.schedule SET template = $3, format = $4, params = $5, title = $6, frequency = $7, weekday = $8,
			month_day = $9, hour = $10, recipients = $11, enabled = $12, next_run_at = $13
		WHERE id = $1 AND user_id = $2 RETURNING `+scheduleColumns,
		*id, userID, in.Template, in.Format, params, in.Title, in.Frequency, in.Weekday, in.MonthDay, in.Hour,
		in.Recipients, in.Enabled, next))
}

// DeleteSchedule удаляет своё расписание (сформированные отчёты остаются).
func (s *Service) DeleteSchedule(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM report.schedule WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RunDue ставит отчёты расписаний, срок которых наступил, и переносит их следующий запуск.
// Возвращает число поставленных отчётов.
func (s *Service) RunDue(ctx context.Context, now time.Time) (int, error) {
	n := 0
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT user_id, `+scheduleColumns+` FROM report.schedule
			WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at FOR UPDATE SKIP LOCKED LIMIT 100`, now)
		if err != nil {
			return err
		}
		type due struct {
			user uuid.UUID
			sc   *Schedule
		}
		var list []due
		for rows.Next() {
			var d due
			var params []byte
			var wd, md *int16
			var hour int16
			var sc Schedule
			if err := rows.Scan(&d.user, &sc.ID, &sc.Template, &sc.Format, &params, &sc.Title, &sc.Frequency, &wd, &md, &hour,
				&sc.Recipients, &sc.Enabled, &sc.NextRunAt, &sc.LastRunAt, &sc.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			sc.Hour = int(hour)
			if wd != nil {
				v := int(*wd)
				sc.Weekday = &v
			}
			if md != nil {
				v := int(*md)
				sc.MonthDay = &v
			}
			if err := json.Unmarshal(params, &sc.Params); err != nil {
				rows.Close()
				return err
			}
			d.sc = &sc
			list = append(list, d)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, d := range list {
			if _, err := s.createReport(ctx, tx, d.user, d.sc.Template, d.sc.Format, d.sc.Params, &d.sc.ID); err != nil {
				return fmt.Errorf("schedule %s: %w", d.sc.ID, err)
			}
			if _, err := tx.Exec(ctx, `UPDATE report.schedule SET last_run_at = $2, next_run_at = $3 WHERE id = $1`,
				d.sc.ID, now, d.sc.NextRun(now)); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

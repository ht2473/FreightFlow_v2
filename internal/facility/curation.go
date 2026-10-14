package facility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
)

// ErrInvalid — неверная правка или сообщение (поле, значение, причина).
var ErrInvalid = errors.New("facility: invalid curation")

// curableColumns — поля, которые уточняет редактор, и колонки записи объекта.
var curableColumns = map[string]string{
	"name":      "name",
	"type_code": "type_code",
	"status":    "status",
	"address":   "official_address",
}

// Curation — правка поля объекта редактором.
type Curation struct {
	ID        uuid.UUID
	Field     string
	Value     *string
	Reason    string
	Author    *string
	CreatedAt time.Time
	RevokedAt *time.Time
	RevokedBy *string
}

// Report — сообщение о неточности записи.
type Report struct {
	ID           uuid.UUID
	FacilityID   uuid.UUID
	FacilityName string
	Message      string
	Author       *string
	Status       string // open, resolved, rejected
	Resolution   *string
	ResolvedBy   *string
	CreatedAt    time.Time
	ResolvedAt   *time.Time
}

// History — правки и сообщения по объекту, новые сверху.
type History struct {
	Curations []Curation
	Reports   []Report
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Curate записывает правку поля и применяет действующие правки к записи объекта.
func (s *Service) Curate(ctx context.Context, facilityID, author uuid.UUID, field string, value *string, reason string) (*Curation, error) {
	col, ok := curableColumns[field]
	if !ok {
		return nil, invalid("поле %q не уточняется", field)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("укажите причину правки")
	}
	if value != nil {
		v := strings.TrimSpace(*value)
		value = &v
	}
	if value == nil || *value == "" {
		return nil, invalid("укажите значение")
	}
	switch field {
	case "status":
		if *value != "active" && *value != "excluded" {
			return nil, invalid("статус — active или excluded")
		}
	case "type_code":
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ref.facility_type WHERE code = $1)`, *value).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, invalid("тип %q неизвестен", *value)
		}
	}
	var id uuid.UUID
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Значение источника — из первой действующей правки поля или из записи до правок.
		var source *string
		err := tx.QueryRow(ctx, `SELECT source_value FROM infra.facility_curation
			WHERE facility_id = $1 AND field = $2 AND revoked_at IS NULL ORDER BY created_at LIMIT 1`, facilityID, field).Scan(&source)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT `+col+`::text FROM infra.facility WHERE id = $1 FOR UPDATE`, facilityID).Scan(&source)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
		}
		if err != nil {
			return err
		}
		id = ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO infra.facility_curation (id, facility_id, field, value, source_value, reason, author_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, facilityID, field, value, source, reason, author); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT infra.apply_curation()`)
		return err
	})
	if err != nil {
		return nil, err
	}
	h, err := s.History(ctx, facilityID)
	if err != nil {
		return nil, err
	}
	for i := range h.Curations {
		if h.Curations[i].ID == id {
			return &h.Curations[i], nil
		}
	}
	return nil, ErrNotFound
}

// RevokeCuration отменяет правку; если по полю действующих правок не осталось, возвращается
// значение источника.
func (s *Service) RevokeCuration(ctx context.Context, facilityID, curationID, by uuid.UUID) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var field string
		var source *string
		err := tx.QueryRow(ctx, `UPDATE infra.facility_curation SET revoked_at = now(), revoked_by = $3
			WHERE id = $1 AND facility_id = $2 AND revoked_at IS NULL RETURNING field, source_value`, curationID, facilityID, by).
			Scan(&field, &source)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var left bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM infra.facility_curation
			WHERE facility_id = $1 AND field = $2 AND revoked_at IS NULL)`, facilityID, field).Scan(&left); err != nil {
			return err
		}
		if !left {
			col := curableColumns[field]
			if _, err := tx.Exec(ctx, `UPDATE infra.facility SET `+col+` = $2, updated_at = now() WHERE id = $1`, facilityID, source); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `SELECT infra.apply_curation()`)
		return err
	})
}

// History возвращает правки и сообщения по объекту.
func (s *Service) History(ctx context.Context, facilityID uuid.UUID) (*History, error) {
	var h History
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.field, c.value, c.reason, a.name, c.created_at, c.revoked_at, r.name
		FROM infra.facility_curation c
		LEFT JOIN core.app_user a ON a.id = c.author_id
		LEFT JOIN core.app_user r ON r.id = c.revoked_by
		WHERE c.facility_id = $1 ORDER BY c.created_at DESC`, facilityID)
	if err != nil {
		return nil, err
	}
	h.Curations, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Curation, error) {
		var c Curation
		return c, r.Scan(&c.ID, &c.Field, &c.Value, &c.Reason, &c.Author, &c.CreatedAt, &c.RevokedAt, &c.RevokedBy)
	})
	if err != nil {
		return nil, err
	}
	h.Reports, err = s.reports(ctx, `WHERE r.facility_id = $1 ORDER BY r.created_at DESC`, facilityID)
	return &h, err
}

func (s *Service) reports(ctx context.Context, where string, args ...any) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.facility_id, coalesce(f.name, t.name), r.message, a.name, r.status, r.resolution, b.name,
		       r.created_at, r.resolved_at
		FROM infra.facility_report r
		JOIN infra.facility f ON f.id = r.facility_id
		JOIN ref.facility_type t ON t.code = f.type_code
		LEFT JOIN core.app_user a ON a.id = r.author_id
		LEFT JOIN core.app_user b ON b.id = r.resolved_by
		`+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Report, error) {
		var v Report
		return v, r.Scan(&v.ID, &v.FacilityID, &v.FacilityName, &v.Message, &v.Author, &v.Status, &v.Resolution,
			&v.ResolvedBy, &v.CreatedAt, &v.ResolvedAt)
	})
}

// ReportIssue записывает сообщение о неточности.
func (s *Service) ReportIssue(ctx context.Context, facilityID, author uuid.UUID, message string) (*Report, error) {
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > 2000 {
		return nil, invalid("опишите неточность (до 2000 знаков)")
	}
	id := ids.New()
	_, err := s.pool.Exec(ctx, `INSERT INTO infra.facility_report (id, facility_id, author_id, message)
		SELECT $1, f.id, $3, $4 FROM infra.facility f WHERE f.id = $2`, id, facilityID, author, message)
	if err != nil {
		return nil, err
	}
	list, err := s.reports(ctx, `WHERE r.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// Reports — очередь сообщений по статусу, старые сверху.
func (s *Service) Reports(ctx context.Context, status string, limit int) ([]Report, error) {
	return s.reports(ctx, `WHERE r.status = $1 ORDER BY r.created_at LIMIT $2`, status, limit)
}

// OpenReports — число открытых сообщений.
func (s *Service) OpenReports(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM infra.facility_report WHERE status = 'open'`).Scan(&n)
	return n, err
}

// ResolveReport закрывает сообщение: resolved (исправлено) или rejected (не подтвердилось).
func (s *Service) ResolveReport(ctx context.Context, reportID, by uuid.UUID, status, resolution string) (*Report, error) {
	if status != "resolved" && status != "rejected" {
		return nil, invalid("статус — resolved или rejected")
	}
	resolution = strings.TrimSpace(resolution)
	if status == "rejected" && resolution == "" {
		return nil, invalid("укажите, почему сообщение не подтвердилось")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE infra.facility_report SET status = $2, resolution = nullif($3, ''), resolved_by = $4,
		resolved_at = now() WHERE id = $1 AND status = 'open'`, reportID, status, resolution, by)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	list, err := s.reports(ctx, `WHERE r.id = $1`, reportID)
	if err != nil || len(list) == 0 {
		return nil, errors.Join(err, ErrNotFound)
	}
	return &list[0], nil
}

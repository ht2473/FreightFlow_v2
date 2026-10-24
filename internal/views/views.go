// Package views — сохранённые виды страниц (карта, реестр): параметры адреса под именем,
// личные или общие для роли.
package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/ids"
)

// ErrNotFound — вида нет или он недоступен.
var ErrNotFound = errors.New("views: not found")

// ErrInvalid — неверное имя, страница или роль.
var ErrInvalid = errors.New("views: invalid")

// Pages — страницы с видами.
var Pages = []string{"map", "facilities"}

// View — сохранённый вид.
type View struct {
	ID        uuid.UUID
	Page      string
	Name      string
	Params    map[string]any
	Role      *string // общий для роли
	Owner     string
	Mine      bool
	CreatedAt time.Time
}

// Service — сохранённые виды.
type Service struct{ pool *pgxpool.Pool }

// New создаёт сервис.
func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

const maxParams = 4096

// List — свои виды страницы и общие для ролей пользователя.
func (s *Service) List(ctx context.Context, user uuid.UUID, roles []string, page string) ([]View, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT v.id, v.page, v.name, v.params, v.role_code, u.name, v.owner_id = $1, v.created_at
		FROM core.saved_view v JOIN core.app_user u ON u.id = v.owner_id
		WHERE v.page = $3 AND (v.owner_id = $1 OR v.role_code = ANY ($2))
		ORDER BY v.owner_id = $1 DESC, v.name`, user, roles, page)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (View, error) {
		var v View
		var params []byte
		if err := r.Scan(&v.ID, &v.Page, &v.Name, &params, &v.Role, &v.Owner, &v.Mine, &v.CreatedAt); err != nil {
			return v, err
		}
		return v, json.Unmarshal(params, &v.Params)
	})
}

// Create сохраняет вид; общий — только для роли самого пользователя.
func (s *Service) Create(ctx context.Context, user uuid.UUID, roles []string, page, name string, params map[string]any, role *string) (*View, error) {
	name = strings.TrimSpace(name)
	if !slices.Contains(Pages, page) || name == "" || len([]rune(name)) > 120 {
		return nil, fmt.Errorf("%w: page or name", ErrInvalid)
	}
	if role != nil && !slices.Contains(roles, *role) {
		return nil, fmt.Errorf("%w: role %s", ErrInvalid, *role)
	}
	b, err := json.Marshal(params)
	if err != nil || len(b) > maxParams {
		return nil, fmt.Errorf("%w: params", ErrInvalid)
	}
	id := ids.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO core.saved_view (id, owner_id, page, name, params, role_code)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, user, page, name, b, role); err != nil {
		return nil, err
	}
	list, err := s.List(ctx, user, roles, page)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// Delete удаляет свой вид.
func (s *Service) Delete(ctx context.Context, user, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.saved_view WHERE id = $1 AND owner_id = $2`, id, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

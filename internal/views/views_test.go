package views

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/platform/dbtest"
)

func TestViews(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	a, b := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.role (code, name) VALUES ('team', 'Команда') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name) VALUES ($1, 'a@example.com', 'Анна'),
		($2, 'b@example.com', 'Борис')`, a, b); err != nil {
		t.Fatal(err)
	}
	svc := New(pool)
	team := "team"
	if _, err := svc.Create(ctx, a, []string{"team"}, "map", " ", nil, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: %v", err)
	}
	if _, err := svc.Create(ctx, a, []string{"analyst"}, "map", "Склады", nil, &team); !errors.Is(err, ErrInvalid) {
		t.Errorf("foreign role: %v", err)
	}
	mine, err := svc.Create(ctx, a, []string{"team"}, "map", "Личный", map[string]any{"l": "facilities", "z": 11.0}, nil)
	if err != nil || !mine.Mine || mine.Params["l"] != "facilities" {
		t.Fatalf("personal: %+v, %v", mine, err)
	}
	if _, err := svc.Create(ctx, a, []string{"team"}, "map", "Общий", nil, &team); err != nil {
		t.Fatal(err)
	}
	// Другой участник роли видит только общий вид и не может его удалить.
	list, err := svc.List(ctx, b, []string{"team"}, "map")
	if err != nil || len(list) != 1 || list[0].Name != "Общий" || list[0].Mine || list[0].Owner != "Анна" {
		t.Fatalf("shared list %+v, %v", list, err)
	}
	if err := svc.Delete(ctx, b, list[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete foreign: %v", err)
	}
	if err := svc.Delete(ctx, a, mine.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.List(ctx, a, []string{"team"}, "facilities"); len(l) != 0 {
		t.Errorf("other page: %d", len(l))
	}
}

package facility

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

func ptr(s string) *string { return &s }

func TestCuration(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	svc := New(pool)
	ctx := t.Context()
	editor, analyst := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name) VALUES ($1, 'ed@example.com', 'Редактор'),
		($2, 'an@example.com', 'Аналитик')`, editor, analyst); err != nil {
		t.Fatal(err)
	}
	page, err := svc.List(ctx, Filter{}, "name", "", 2)
	if err != nil || len(page.Items) < 2 {
		t.Fatalf("list: %v", err)
	}
	it := page.Items[0]
	origName := ""
	if it.Name != nil {
		origName = *it.Name
	}

	// Проверки правки: поле, значение, причина, тип из справочника.
	for _, c := range []struct{ field, value, reason string }{
		{"confidence", "1", "ошибка"}, {"name", "Склад", ""}, {"status", "deleted", "дубль"}, {"type_code", "factory", "ошибка"},
	} {
		if _, err := svc.Curate(ctx, it.ID, editor, c.field, ptr(c.value), c.reason); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}

	// Правка названия и исключение: запись меняется, поля помечены, причина и автор в истории.
	if _, err := svc.Curate(ctx, it.ID, editor, "name", ptr("Склад по договору аренды"), "уточнено по договору"); err != nil {
		t.Fatal(err)
	}
	ex, err := svc.Curate(ctx, page.Items[1].ID, editor, "status", ptr("excluded"), "не логистический объект")
	if err != nil {
		t.Fatal(err)
	}
	card, err := svc.Get(ctx, it.ID)
	if err != nil || card.Name == nil || *card.Name != "Склад по договору аренды" || !slices.Contains(card.Curated, "name") {
		t.Fatalf("curated card: %+v, %v", card, err)
	}
	h, err := svc.History(ctx, it.ID)
	if err != nil || len(h.Curations) != 1 || h.Curations[0].Author == nil || *h.Curations[0].Author != "Редактор" {
		t.Fatalf("history %+v, %v", h, err)
	}

	// Повторный перенос OSM не отменяет правки.
	osmtest.Transform(t, pool)
	card, err = svc.Get(ctx, it.ID)
	if err != nil || card.Name == nil || *card.Name != "Склад по договору аренды" {
		t.Fatalf("after reload: %v, %v", card.Name, err)
	}
	if c, err := svc.Get(ctx, page.Items[1].ID); err != nil || c.Status != "excluded" {
		t.Fatalf("excluded after reload: %v, %v", c.Status, err)
	}

	// Отмена: значение источника возвращается.
	if err := svc.RevokeCuration(ctx, it.ID, h.Curations[0].ID, editor); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeCuration(ctx, page.Items[1].ID, ex.ID, editor); err != nil {
		t.Fatal(err)
	}
	card, _ = svc.Get(ctx, it.ID)
	back, _ := svc.Get(ctx, page.Items[1].ID)
	if card.Name == nil && origName != "" || card.Name != nil && *card.Name != origName || len(card.Curated) != 0 || back.Status != "active" {
		t.Fatalf("revoked: name %v (want %q), curated %v, status %s", card.Name, origName, card.Curated, back.Status)
	}

	// Сообщение о неточности: в очереди, закрывается редактором.
	if _, err := svc.ReportIssue(ctx, it.ID, analyst, "  "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty report: %v", err)
	}
	rep, err := svc.ReportIssue(ctx, it.ID, analyst, "Склад закрыт с 2025 года")
	if err != nil || rep.Status != "open" || rep.Author == nil {
		t.Fatalf("report %+v, %v", rep, err)
	}
	if n, err := svc.OpenReports(ctx); err != nil || n != 1 {
		t.Fatalf("open reports %d, %v", n, err)
	}
	if _, err := svc.ResolveReport(ctx, rep.ID, editor, "rejected", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("reject without reason: %v", err)
	}
	done, err := svc.ResolveReport(ctx, rep.ID, editor, "resolved", "объект исключён")
	if err != nil || done.Status != "resolved" || done.ResolvedBy == nil {
		t.Fatalf("resolve %+v, %v", done, err)
	}
	if _, err := svc.ResolveReport(ctx, rep.ID, editor, "resolved", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolve twice: %v", err)
	}
	if q, err := svc.Reports(ctx, "open", 10); err != nil || len(q) != 0 {
		t.Errorf("queue %d, %v", len(q), err)
	}
}

package facility

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

func TestListFacetsCard(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	svc := New(pool)
	ctx := t.Context()

	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM infra.facility WHERE status = 'active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	first, err := svc.List(ctx, Filter{}, "", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != active || len(first.Items) != min(5, active) || active > 5 && first.Next == "" {
		t.Fatalf("page: total %d of %d, %d items, next %q", first.Total, active, len(first.Items), first.Next)
	}
	// Сортировка по умолчанию — по достоверности; следующая страница продолжает без повторов.
	for i := 1; i < len(first.Items); i++ {
		if first.Items[i].Confidence > first.Items[i-1].Confidence {
			t.Fatalf("not sorted by confidence: %v", first.Items)
		}
	}
	second, err := svc.List(ctx, Filter{}, "", first.Next, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range second.Items {
		if slices.ContainsFunc(first.Items, func(x Item) bool { return x.ID == it.ID }) {
			t.Fatalf("repeated %s", it.ID)
		}
	}

	// Фасет типа считает все типы, даже при фильтре по типу; остальные фасеты — с фильтром.
	facets, err := svc.Facets(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, v := range facets.Types {
		sum += v.Count
	}
	if sum != active || len(facets.Types) == 0 || len(facets.Confidence) == 0 || facets.Confidence[0].Label == "" {
		t.Fatalf("facets %+v", facets)
	}
	one := facets.Types[0]
	filtered, err := svc.Facets(ctx, Filter{Types: []string{one.Value}})
	if err != nil {
		t.Fatal(err)
	}
	conf := 0
	for _, v := range filtered.Confidence {
		conf += v.Count
	}
	if len(filtered.Types) != len(facets.Types) || conf != one.Count {
		t.Errorf("filtered facets: %d types, confidence sum %d (want %d)", len(filtered.Types), conf, one.Count)
	}
	page, err := svc.List(ctx, Filter{Types: []string{one.Value}}, "name", "", 200)
	if err != nil || page.Total != one.Count || slices.ContainsFunc(page.Items, func(x Item) bool { return x.Type != one.Value }) {
		t.Fatalf("type filter: %d of %d, %v", page.Total, one.Count, err)
	}

	// Рамка вокруг первого объекта находит его; подстрока названия — объекты с ней.
	it := first.Items[0]
	box, err := svc.List(ctx, Filter{BBox: []float64{it.Lon - 0.001, it.Lat - 0.001, it.Lon + 0.001, it.Lat + 0.001}}, "", "", 50)
	if err != nil || !slices.ContainsFunc(box.Items, func(x Item) bool { return x.ID == it.ID }) {
		t.Fatalf("bbox: %v, %v", box.Total, err)
	}
	if it.Name != nil {
		name := []rune(*it.Name)
		q, err := svc.List(ctx, Filter{Q: string(name[:min(5, len(name))])}, "", "", 50)
		if err != nil || q.Total == 0 {
			t.Fatalf("q: %d, %v", q.Total, err)
		}
	}
	if _, err := svc.List(ctx, Filter{}, "price", "", 10); !errors.Is(err, ErrBadFilter) {
		t.Errorf("bad sort: %v", err)
	}
	if _, err := svc.List(ctx, Filter{BBox: []float64{1, 2}}, "", "", 10); !errors.Is(err, ErrBadFilter) {
		t.Errorf("bad bbox: %v", err)
	}

	// Карточка: строка реестра, геометрия и связанные данные.
	card, err := svc.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.ID != it.ID || card.Status != "active" || card.OSMID == 0 || len(card.ConfidenceReasons) == 0 ||
		card.Footprint == nil && card.Territory == nil {
		t.Errorf("card %+v", card)
	}
	if _, err := svc.Get(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	cards, err := svc.Compare(ctx, []uuid.UUID{first.Items[0].ID, first.Items[1].ID})
	if err != nil || len(cards) != 2 || cards[1].ID != first.Items[1].ID {
		t.Errorf("compare: %v", err)
	}
	if _, err := svc.Compare(ctx, []uuid.UUID{it.ID}); !errors.Is(err, ErrBadFilter) {
		t.Errorf("compare one: %v", err)
	}
}

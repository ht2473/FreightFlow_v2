package routing

import (
	"slices"
	"testing"

	"freightflow/internal/platform/dbtest"
)

func TestSplitQuery(t *testing.T) {
	t.Parallel()
	words, numbers := splitQuery("Газгольдерная ул., д. 10к2, стр 2/1")
	if words != "газгольдерная ул стр" || !slices.Equal(numbers, []string{"10к2", "2/1"}) {
		t.Errorf("words %q, numbers %v", words, numbers)
	}
}

func TestPlaces(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO territory.address (unom, mos_global_id, address, simple_address, obj_type, point, source_code)
		SELECT 100 + n, 100 + n, 'Москва, ' || a, a, t, ST_SetSRID(ST_MakePoint(37.73 + n / 1000.0, 55.73), 4326), 'test'
		FROM (VALUES (1, 'Газгольдерная улица, дом 106', 'здание'), (2, 'Газгольдерная улица, дом 6', 'здание'),
		             (3, 'Газгольдерная улица, дом 10', 'здание'), (4, 'Рязанский проспект, дом 10', 'здание')) v(n, a, t);
		INSERT INTO infra.facility (id, type_code, name, point, status, source_code, confidence, osm_type, osm_id)
		VALUES (uuidv7(), 'warehouse', 'Склад на Газгольдерной', ST_SetSRID(ST_MakePoint(37.731, 55.729), 4326),
		        'active', 'test', 0.8, 'W', 1)`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, nil)
	got, err := svc.Places(t.Context(), "газгольдерная 10", 5)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, p := range got {
		titles = append(titles, p.Kind+": "+p.Title)
	}
	// Объект по названию, затем адреса: с номером дома из запроса — первым.
	want := []string{
		"facility: Склад на Газгольдерной", "address: Газгольдерная улица, дом 10",
	}
	if len(got) < 3 || !slices.Equal(titles[:2], want) || slices.Contains(titles, "address: Рязанский проспект, дом 10") {
		t.Errorf("places %v", titles)
	}
	if got[1].Point.Lon == 0 || got[1].Subtitle != "здание" {
		t.Errorf("address place %+v", got[1])
	}
	// Без слов (только номер) — пусто.
	if got, err := svc.Places(t.Context(), "10", 5); err != nil || len(got) != 0 {
		t.Errorf("number only: %v, %v", got, err)
	}
}

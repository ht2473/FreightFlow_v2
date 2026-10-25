package search

import (
	"errors"
	"testing"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

func TestReindex(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	svc := New(pool)

	res, err := svc.Reindex(ctx, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Indexed{}
	for _, r := range res {
		got[r.Kind] = r
	}
	if len(got) != len(Builders) || got["facility"].Documents == 0 || got["territory"].Documents == 0 {
		t.Fatalf("первое построение: %+v", res)
	}

	// Без изменений данных вид пропускается; правка объекта меняет подпись.
	again, err := svc.Reindex(ctx, []string{"facility", "territory"}, false)
	if err != nil || !again[0].Skipped || !again[1].Skipped {
		t.Fatalf("повтор: %+v %v", again, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE infra.facility SET name = 'Склад «Тестовый»', updated_at = now()
		WHERE id = (SELECT id FROM infra.facility WHERE status = 'active' LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	again, err = svc.Reindex(ctx, []string{"facility"}, false)
	if err != nil || again[0].Skipped {
		t.Fatalf("после правки: %+v %v", again, err)
	}

	// Документ объекта: синонимы типа, округ сокращённо, поиск по стемам и без ё.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM search.document
		WHERE kind = 'facility' AND tsv @@ websearch_to_tsquery('search.ru', 'тестовый склады')`).Scan(&n); err != nil || n != 1 {
		t.Errorf("полнотекстовый по названию: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM search.document
		WHERE kind = 'facility' AND tsv @@ websearch_to_tsquery('search.ru', 'складской комплекс')`).Scan(&n); err != nil || n == 0 {
		t.Errorf("синонимы типа: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM search.document
		WHERE kind = 'territory' AND title_norm % 'нижегородскии раион'`).Scan(&n); err != nil || n == 0 {
		t.Errorf("триграммы с опечаткой: %d %v", n, err)
	}

	if _, err := svc.Reindex(ctx, []string{"planet"}, false); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("неизвестный вид: %v", err)
	}
}

package osm

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
)

// loadStage заполняет osm_stage фрагментом testdata/stage.sql.gz (Нижегородский район;
// данные © участники OpenStreetMap, ODbL 1.0; как получен — testdata/make_stage.sql).
func loadStage(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	f, err := os.Open("testdata/stage.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
		t.Fatal(err)
	}
}

func runTransform(t *testing.T, pool *pgxpool.Pool) ingest.LoadStats {
	t.Helper()
	var stats ingest.LoadStats
	err := db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		var err error
		stats, err = transform(t.Context(), tx, uuid.New())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestTransformDistrict(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	loadStage(t, pool)

	st := runTransform(t, pool)
	want := map[string]int64{"regions": 2, "okrugs": 1, "districts": 1}
	for k, v := range want {
		if st[k] != v {
			t.Errorf("%s = %d, want %d", k, st[k], v)
		}
	}
	// Район привязан к округу, округ — к городу.
	if n := count(t, pool, `
		SELECT count(*) FROM territory.admin_area d
		JOIN territory.admin_area o ON o.id = d.parent_id AND o.short_name = 'ЮВАО'
		JOIN territory.admin_area r ON r.id = o.parent_id AND r.name = 'Москва'
		WHERE d.name = 'Нижегородский район'`); n != 1 {
		t.Fatalf("district hierarchy: %d", n)
	}

	// Объекты логистики: есть, у каждого — тип, достоверность с причинами, район и округ.
	if st["records"] < 15 {
		t.Fatalf("facilities: %d", st["records"])
	}
	if n := count(t, pool, `
		SELECT count(*) FROM infra.facility
		WHERE status = 'active' AND (okrug_id IS NULL OR confidence <= 0 OR jsonb_array_length(confidence_reasons) = 0)
		  AND ST_Contains((SELECT geom FROM territory.admin_area WHERE name = 'Нижегородский район'), point)`); n != 0 {
		t.Fatalf("facilities without territory or confidence reasons: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM infra.facility WHERE floor_area_m2 < building_area_m2`); n != 0 {
		t.Fatalf("floor area below footprint: %d", n)
	}
	// Объект, лежащий в контуре большего объекта больше чем наполовину, — его часть, не отдельный объект.
	if n := count(t, pool, `
		SELECT count(*) FROM infra.facility c JOIN infra.facility o ON o.id <> c.id
		WHERE ST_Intersects(coalesce(o.territory, o.footprint), c.point)
		  AND ST_Area(coalesce(o.territory, o.footprint)) > coalesce(ST_Area(coalesce(c.territory, c.footprint)), 0)
		  AND (coalesce(c.territory, c.footprint) IS NULL
		       OR ST_Area(ST_Intersection(coalesce(o.territory, o.footprint), coalesce(c.territory, c.footprint)))
		          > 0.5 * ST_Area(coalesce(c.territory, c.footprint)))`); n != 0 {
		t.Fatalf("nested facilities: %d", n)
	}

	// Сеть: участки, ограничения из тегов, мосты; промзоны.
	if st["roads"] < 500 || st["restrictions"] == 0 || st["industrial_zones"] == 0 {
		t.Fatalf("network: %v", st)
	}
	if n := count(t, pool, `SELECT count(*) FROM network.restriction WHERE kind = 'maxweight' AND (value IS NULL OR unit <> 't')`); n != 0 {
		t.Fatalf("maxweight without value: %d", n)
	}

	// Повторный перенос того же снимка: идентификаторы и даты появления не меняются.
	before := map[string]string{}
	rows, err := pool.Query(t.Context(), `SELECT osm_type || osm_id, id::text FROM infra.facility`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		before[k] = v
	}
	rows.Close()
	again := runTransform(t, pool)
	if again["facilities_new"] != 0 || again["records"] != st["records"] {
		t.Fatalf("second run: %v", again)
	}
	var changed int
	for k, v := range before {
		var id string
		if err := pool.QueryRow(t.Context(), `SELECT id::text FROM infra.facility WHERE osm_type || osm_id = $1`, k).Scan(&id); err != nil || id != v {
			changed++
		}
	}
	if changed != 0 {
		t.Fatalf("%d facility ids changed between runs", changed)
	}

	// Объект пропал из источника — запись остаётся со статусом «missing».
	var osmType string
	var osmID int64
	if err := pool.QueryRow(t.Context(),
		`SELECT osm_type, osm_id FROM infra.facility WHERE osm_type <> 'N' ORDER BY building_area_m2 DESC NULLS LAST LIMIT 1`).
		Scan(&osmType, &osmID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM osm_stage.areas WHERE osm_type = $1 AND osm_id = $2`, osmType, osmID); err != nil {
		t.Fatal(err)
	}
	runTransform(t, pool)
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM infra.facility WHERE osm_type = $1 AND osm_id = $2`, osmType, osmID).
		Scan(&status); err != nil || status != "missing" {
		t.Fatalf("removed facility: status %q, %v", status, err)
	}
}

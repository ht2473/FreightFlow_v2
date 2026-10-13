package mosdata

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
)

// Фрагменты — Нижегородский район: сеть из фрагмента OSM (../osm/testdata), записи наборов
// портала в его границах (testdata/make_records.sql).

func gunzip(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// loadNetwork переносит фрагмент OSM в домен (дорожная сеть, ограничения OSM, территории).
func loadNetwork(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), string(gunzip(t, "../osm/testdata/stage.sql.gz"))); err != nil {
		t.Fatal(err)
	}
	script, err := etl.FS.ReadFile("osm/transform.sql")
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('ff.source', 'osm', true), set_config('ff.snapshot', $1, true)`,
			uuid.NewString()); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), string(script))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func dataset(t *testing.T, code string) Dataset {
	t.Helper()
	for _, ds := range Datasets {
		if ds.Code == code {
			return ds
		}
	}
	t.Fatalf("no dataset %s", code)
	return Dataset{}
}

func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	var out []Record
	sc := bufio.NewScanner(bytes.NewReader(gunzip(t, path)))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// load переносит записи набора в домен так же, как загрузка: стадия, затем скрипт набора.
func load(t *testing.T, pool *pgxpool.Pool, ds Dataset, recs []Record) ingest.LoadStats {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	script, err := etl.FS.ReadFile("mosdata/" + ds.Transform)
	if err != nil {
		t.Fatal(err)
	}
	var stats ingest.LoadStats
	err = db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		if _, err := stage(t.Context(), tx, ds.ID, &buf); err != nil {
			return err
		}
		stats, err = transform(t.Context(), tx, ds, uuid.New(), string(script))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

// setCells возвращает атрибуты записи с заменёнными полями (nil — пустое значение).
func setCells(t *testing.T, raw json.RawMessage, set map[string]any) json.RawMessage {
	t.Helper()
	var cells map[string]any
	if err := json.Unmarshal(raw, &cells); err != nil {
		t.Fatal(err)
	}
	for k, v := range set {
		cells[k] = v
	}
	b, err := json.Marshal(cells)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestSignsMatchDistrict(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	loadNetwork(t, pool)
	recs := readRecords(t, "testdata/signs.jsonl.gz")
	st := load(t, pool, dataset(t, "mos_road_signs"), recs)
	t.Logf("stats: %v", st)

	if st["signs_active"] != int64(len(recs)) {
		t.Errorf("signs_active = %d, want %d", st["signs_active"], len(recs))
	}
	// Знаки ограничений в границах фрагмента сети в основном привязываются к участкам.
	if st["signs_match_pct"] < 80 {
		t.Errorf("signs_match_pct = %d, want ≥ 80", st["signs_match_pct"])
	}
	// Привязка — не дальше 25 м от участка; ограничения из знаков — на привязанных участках.
	if n := count(t, pool, `
		SELECT count(*) FROM network.road_sign s JOIN network.road_segment r ON r.id = s.segment_id
		WHERE NOT ST_DWithin(s.point::geography, r.geom::geography, 25.01)`); n != 0 {
		t.Errorf("%d signs matched farther than 25 m", n)
	}
	if st["sign_restrictions"] == 0 {
		t.Error("no restrictions from signs")
	}
	if n := count(t, pool, `
		SELECT count(*) FROM network.restriction r
		LEFT JOIN network.road_sign s ON s.segment_id = r.segment_id AND s.status = 'active'
		WHERE r.origin = 'sign' AND s.id IS NULL`); n != 0 {
		t.Errorf("%d sign restrictions without a matched sign", n)
	}
	// Действующее ограничение участка берётся из знака раньше, чем из OSM.
	if n := count(t, pool, `
		SELECT count(*) FROM network.restriction_effective e
		WHERE e.origin = 'osm' AND EXISTS (
			SELECT 1 FROM network.restriction r WHERE r.segment_id = e.segment_id AND r.kind = e.kind AND r.origin = 'sign')`); n != 0 {
		t.Errorf("%d effective OSM restrictions shadow sign restrictions", n)
	}
	if st["conflicts_sign_not_in_osm"]+st["conflicts_osm_not_signed"] == 0 {
		t.Error("no conflicts between signs and OSM in the district")
	}

	// Повторная загрузка тех же записей ничего не меняет: знаки не сдвигаются и не привязываются заново.
	again := load(t, pool, dataset(t, "mos_road_signs"), recs)
	for _, k := range []string{"signs_active", "sign_restrictions", "conflicts_sign_not_in_osm", "conflicts_osm_not_signed"} {
		if again[k] != st[k] {
			t.Errorf("reload %s = %d, want %d", k, again[k], st[k])
		}
	}
	if again["signs_new"] != 0 || again["signs_missing"] != 0 {
		t.Errorf("reload: new %d, missing %d", again["signs_new"], again["signs_missing"])
	}
}

func TestRoadWorksLifecycle(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	loadNetwork(t, pool)
	ds := dataset(t, "mos_works_planned")
	recs := readRecords(t, "testdata/works.jsonl.gz")
	n := int64(len(recs))
	// Сроки и статус переносятся в будущее, чтобы состояние не зависело от дня запуска теста.
	for i := range recs {
		recs[i].Cells = setCells(t, recs[i].Cells, map[string]any{
			"WorksBeginDate": "01.06.2099", "PlannedEndDate": "30.06.2099", "ActualBeginDate": nil, "ActualEndDate": nil,
			"WorksStatus": "запланированы",
		})
	}

	st := load(t, pool, ds, recs)
	if st["works"] != n || st["works_appeared"] != n || st["works_changed"] != 0 || st["works_planned"] != n {
		t.Fatalf("first load: %v (records %d)", st, n)
	}
	if st["works_without_geometry"] != 0 {
		t.Errorf("works_without_geometry = %d", st["works_without_geometry"])
	}
	// Работы на улицах района затрагивают участки сети.
	if c := count(t, pool, `SELECT count(DISTINCT work_id) FROM operations.road_work_impact`); c < n/2 {
		t.Errorf("works with impact = %d of %d", c, n)
	}

	// Вторая загрузка: у одной записи сдвинут срок окончания, другая исчезла с портала.
	changed, gone := recs[0], recs[1]
	moved := Record{ID: changed.ID, Cells: setCells(t, changed.Cells, map[string]any{"PlannedEndDate": "31.12.2099"}), Geometry: changed.Geometry}
	second := append([]Record{moved}, recs[2:]...)
	st = load(t, pool, ds, second)
	if st["works"] != n-1 || st["works_appeared"] != 0 || st["works_changed"] != 1 || st["works_removed"] != 1 {
		t.Fatalf("second load: %v", st)
	}
	if c := count(t, pool, `SELECT count(*) FROM operations.road_work WHERE mos_global_id = $1 AND state = 'removed'`, gone.ID); c != 1 {
		t.Error("removed work is not in state removed")
	}
	if c := count(t, pool, `
		SELECT count(*) FROM operations.road_work_event e JOIN operations.road_work w ON w.id = e.work_id
		WHERE w.mos_global_id = $1 AND e.type = 'removed'`, gone.ID); c != 1 {
		t.Errorf("removed events = %d, want 1", c)
	}

	// Третья загрузка: запись вернулась — снова «появилась».
	st = load(t, pool, ds, recs)
	if st["works"] != n || st["works_appeared"] != 1 || st["works_changed"] != 1 {
		t.Fatalf("third load: %v", st)
	}

	// Срок прошёл — пересчёт состояний переводит работу в «завершена» с событием.
	if _, err := pool.Exec(t.Context(), `UPDATE operations.road_work SET period = tstzrange('2020-01-01', '2020-02-01')
		WHERE mos_global_id = $1`, changed.ID); err != nil {
		t.Fatal(err)
	}
	var transitions int64
	if err := pool.QueryRow(t.Context(), `SELECT operations.refresh_work_states()`).Scan(&transitions); err != nil {
		t.Fatal(err)
	}
	if transitions < 1 {
		t.Errorf("transitions = %d", transitions)
	}
	if c := count(t, pool, `
		SELECT count(*) FROM operations.road_work w JOIN operations.road_work_event e ON e.work_id = w.id AND e.type = 'finished'
		WHERE w.mos_global_id = $1 AND w.state = 'finished'`, changed.ID); c != 1 {
		t.Error("work past its period is not finished")
	}
}

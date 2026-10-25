package analytics

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

var validLevels = []string{LevelFacility, LevelDistrict, LevelOkrug, LevelSegment, LevelCity, LevelH3r8, LevelH3r9}

func TestCatalogIsConsistent(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, m := range Catalog() {
		if seen[m.Code] {
			t.Errorf("повтор кода %s", m.Code)
		}
		seen[m.Code] = true
		if m.Title == "" || m.Unit == "" || m.Formula == "" || m.Method == "" || m.Compute == "" || m.Version < 1 {
			t.Errorf("%s: неполное описание", m.Code)
		}
		if len(m.Domains) == 0 && len(m.DependsOn) == 0 {
			t.Errorf("%s: нет входов — не будет пересчитываться", m.Code)
		}
		for _, l := range m.Levels {
			if !slices.Contains(validLevels, l) {
				t.Errorf("%s: уровень %q", m.Code, l)
			}
		}
		if !strings.HasPrefix(m.Code, strings.SplitN(m.Code, ".", 2)[0]+".") {
			t.Errorf("%s: код без группы", m.Code)
		}
	}
	list, err := ordered(Catalog())
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, m := range list {
		pos[m.Code] = i
	}
	for _, m := range list {
		for _, d := range m.DependsOn {
			if pos[d] >= pos[m.Code] {
				t.Errorf("%s считается раньше зависимости %s", m.Code, d)
			}
		}
	}
}

func TestOrderedDetectsCycleAndMissing(t *testing.T) {
	t.Parallel()
	_, err := ordered([]Metric{{Code: "a", DependsOn: []string{"b"}}, {Code: "b", DependsOn: []string{"a"}}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("цикл: %v", err)
	}
	if _, err := ordered([]Metric{{Code: "a", DependsOn: []string{"x"}}}); !errors.Is(err, ErrUnknown) {
		t.Errorf("нет зависимости: %v", err)
	}
}

func TestRecompute(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	svc := New(pool)

	res, err := svc.Recompute(ctx, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]int{}
	for _, r := range res {
		rows[r.Code] = r.Rows
	}
	if len(res) != len(Catalog()) || rows["facility.restrictions_300m"] == 0 || rows["territory.facilities"] == 0 ||
		rows["territory.network_km"] == 0 || rows["territory.restricted_share"] == 0 {
		t.Fatalf("первый расчёт: %v", rows)
	}

	// Входы не изменились — пересчёта нет; новая версия домена — пересчёт зависимых.
	again, err := svc.Recompute(ctx, []string{"territory.facilities"}, false)
	if err != nil || !again[0].Skipped {
		t.Fatalf("повтор: %+v %v", again, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ingest.data_version (domain, version) VALUES ('facility', 1000)
		ON CONFLICT (domain) DO UPDATE SET version = 1000`); err != nil {
		t.Fatal(err)
	}
	again, err = svc.Recompute(ctx, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	recomputed := map[string]bool{}
	for _, r := range again {
		recomputed[r.Code] = !r.Skipped
	}
	if !recomputed["territory.facilities"] || !recomputed["territory.warehouse_density"] || recomputed["territory.network_km"] {
		t.Errorf("пересчёт по домену facility: %v", recomputed)
	}

	// Объекты округа — по привязке реестра; доля — отношение показателей.
	var okrug, direct float64
	if err := pool.QueryRow(ctx, `
		SELECT o.value, (SELECT count(*) FROM infra.facility f WHERE f.status = 'active' AND f.okrug_id::text = o.entity)
		FROM analytics.metric_value o WHERE o.metric_code = 'territory.facilities' AND o.level = 'okrug' AND o.value > 0 LIMIT 1`).
		Scan(&okrug, &direct); err != nil {
		t.Fatal(err)
	}
	if okrug != direct {
		t.Errorf("объекты округа %v, по реестру %v", okrug, direct)
	}
	var share, restricted, network float64
	if err := pool.QueryRow(ctx, `
		SELECT s.value, r.value, n.value FROM analytics.metric_value s
		JOIN analytics.metric_value r ON r.metric_code = 'territory.restricted_km' AND r.level = s.level AND r.entity = s.entity
		JOIN analytics.metric_value n ON n.metric_code = 'territory.network_km' AND n.level = s.level AND n.entity = s.entity
		WHERE s.metric_code = 'territory.restricted_share' AND s.level = 'city'`).Scan(&share, &restricted, &network); err != nil {
		t.Fatal(err)
	}
	if diff := share - restricted/network*100; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("доля %v, ограничения %v, сеть %v", share, restricted, network)
	}
	var defs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.metric_def`).Scan(&defs); err != nil || defs != len(Catalog()) {
		t.Errorf("каталог в базе: %d %v", defs, err)
	}
}

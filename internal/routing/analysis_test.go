package routing

import (
	"math"
	"testing"

	"freightflow/internal/platform/dbtest"
)

// tileOf — тайл масштаба z с точкой.
func tileOf(p Point, z int) (x, y int) {
	n := math.Exp2(float64(z))
	lat := p.Lat * math.Pi / 180
	return int((p.Lon + 180) / 360 * n), int((1 - math.Asinh(math.Tan(lat))/math.Pi) / 2 * n)
}

func TestAnalyzeNetwork(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	loadDistrict(t, pool)
	// Колец во фрагменте нет: «точки МКАД» — начала трёх магистралей района.
	if _, err := pool.Exec(t.Context(), `INSERT INTO analytics.accessibility_target (kind, name, point)
		SELECT 'mkad', 'МКАД', ST_StartPoint(geom) FROM network.road_segment
		WHERE class IN ('trunk', 'primary') ORDER BY osm_id LIMIT 3`); err != nil {
		t.Fatal(err)
	}
	srv := fakeValhalla(t)
	defer srv.Close()
	svc := NewService(pool, NewClient(srv.URL))
	opts := CentralityOptions{Batch: 32, MinSources: 128, MaxSources: 256, TargetRSE: 0.1, Top: 50, Seed: 1, Workers: 4}

	st, err := svc.AnalyzeNetwork(t.Context(), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Nodes < 500 || st.Edges < st.Nodes/2 || st.Arcs < st.Edges || st.LargestComponent < 0.5 {
		t.Fatalf("graph %+v", st)
	}
	// Фрагмент больше предела источников — оценка выборочная, с контролем точности.
	if st.Profile.Exact || st.Profile.Sources < 128 || st.Profile.RSE == nil || st.Profile.SplitHalf == nil {
		t.Fatalf("accuracy %+v", st.Profile)
	}

	var edges, metrics, first, bottlenecks int
	var maxShare, firstShare float64
	if err := pool.QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM network.graph_edge WHERE version_id = $1),
		       count(*), count(*) FILTER (WHERE rank = 1), count(*) FILTER (WHERE bottleneck),
		       max(centrality), max(centrality) FILTER (WHERE rank = 1)
		FROM network.edge_metric WHERE version_id = $1`, st.Version).
		Scan(&edges, &metrics, &first, &bottlenecks, &maxShare, &firstShare); err != nil {
		t.Fatal(err)
	}
	if edges != st.Edges || metrics != st.Edges || first != 1 || firstShare != maxShare || maxShare <= 0 ||
		bottlenecks != st.Bottlenecks {
		t.Fatalf("edges %d, metrics %d, first %d (%g of %g), bottlenecks %d vs %d",
			edges, metrics, first, firstShare, maxShare, bottlenecks, st.Bottlenecks)
	}
	// Узкое место закрыто профилю только пределом массы или габарита.
	var wrong int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM network.edge_metric m
		JOIN network.graph_edge e ON e.version_id = m.version_id AND e.id = m.edge_id
		WHERE m.version_id = $1 AND m.bottleneck AND NOT (e.dims_only AND e.closed_value IS NOT NULL AND m.free_centrality > 0)`,
		st.Version).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatalf("bottlenecks without dimension limit: %d, %v", wrong, err)
	}

	// Критичность: объекты привязаны к графу; места подряд, первое — с наибольшим ущербом;
	// верхние элементы проверены матрицами (заглушка не учитывает исключение — прироста нет).
	cs := st.Criticality
	if cs.FacilitiesSnapped == 0 || cs.TargetsSnapped != 3 || cs.Elements == 0 || cs.Confirmed == 0 {
		t.Fatalf("criticality %+v", cs)
	}
	var elements, maxRank, damaged, firstDamaged, confirmedDamage int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*), max(rank), count(*) FILTER (WHERE facilities > 0 OR disconnected > 0),
		       count(*) FILTER (WHERE rank = 1 AND (facilities > 0 OR disconnected > 0)),
		       count(*) FILTER (WHERE confirmed AND valhalla_damage_min > 0)
		FROM network.element_criticality WHERE version_id = $1`, st.Version).
		Scan(&elements, &maxRank, &damaged, &firstDamaged, &confirmedDamage); err != nil {
		t.Fatal(err)
	}
	if elements != cs.Elements || maxRank != elements || damaged == 0 || firstDamaged != 1 || confirmedDamage != 0 {
		t.Fatalf("elements %d (max rank %d), damaged %d, first damaged %d, confirmed with damage %d",
			elements, maxRank, damaged, firstDamaged, confirmedDamage)
	}

	sum, err := svc.NetworkAnalysis(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Version != st.Version || len(sum.TopStreets) == 0 || sum.TopStreets[0].BestRank < 1 ||
		sum.TopStreets[0].Name == "" || sum.BottleneckCount < len(sum.Bottlenecks) {
		t.Fatalf("summary %+v", sum)
	}

	// Тайл с самым значимым ребром непуст.
	x, y := tileOf(sum.TopStreets[0].Point, 12)
	var tile []byte
	if err := pool.QueryRow(t.Context(), `SELECT tiles.edge_centrality(12, $1, $2)`, x, y).Scan(&tile); err != nil || len(tile) == 0 {
		t.Fatalf("centrality tile %d/%d: %d bytes, %v", x, y, len(tile), err)
	}

	// Повторный расчёт — новая версия; хранятся две последние, домен опубликован дважды.
	for range 2 {
		if st, err = svc.AnalyzeNetwork(t.Context(), opts, nil); err != nil {
			t.Fatal(err)
		}
	}
	var versions, oldest int
	var published int64
	if err := pool.QueryRow(t.Context(), `SELECT count(*), min(id),
		(SELECT version FROM ingest.data_version WHERE domain = 'network_analysis') FROM network.graph_version`).
		Scan(&versions, &oldest, &published); err != nil {
		t.Fatal(err)
	}
	if versions != keptVersions || oldest != st.Version-1 || published != 3 {
		t.Errorf("versions %d (oldest %d, current %d), published %d", versions, oldest, st.Version, published)
	}
}

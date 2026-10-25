package analytics

import (
	"errors"
	"testing"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

func TestHotspotsAndClusters(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	svc := New(pool)
	if _, err := svc.Recompute(ctx, nil, false); err != nil {
		t.Fatal(err)
	}

	hs, err := svc.Hotspots(ctx, HotspotParams{Metric: "h3.facilities", Level: LevelH3r8, K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if hs.Cells == 0 || hs.Hot == 0 || hs.SD <= 0 {
		t.Fatalf("горячие точки: %+v", hs)
	}
	for _, h := range hs.Significant {
		if h.Sign > 0 && h.Z < 1.645 || h.Sign < 0 && h.Z > -1.645 || len(h.Geometry) == 0 {
			t.Fatalf("ячейка %+v", h)
		}
	}
	// Горячая точка — там, где объекты: в диске первой ячейки значение выше среднего.
	if top := hs.Significant[0]; top.Sign != 1 || top.Density <= 0 {
		t.Errorf("первая ячейка %+v", top)
	}
	for _, bad := range []HotspotParams{
		{Metric: "territory.facilities", Level: LevelH3r8, K: 1},
		{Metric: "h3.facilities", Level: LevelDistrict, K: 1},
		{Metric: "h3.facilities", Level: LevelH3r8, K: 5},
	} {
		if _, err := svc.Hotspots(ctx, bad); err == nil {
			t.Errorf("неверные параметры приняты: %+v", bad)
		}
	}

	cl, err := svc.Clusters(ctx, ClusterParams{EpsM: 400, MinPoints: 3})
	if err != nil {
		t.Fatal(err)
	}
	var active, inClusters int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM infra.facility WHERE status = 'active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	for _, c := range cl.Clusters {
		inClusters += c.Facilities
		if c.Facilities < 3 || len(c.Hull) == 0 || len(c.Names) == 0 {
			t.Errorf("кластер %+v", c)
		}
	}
	if cl.Total != active || inClusters+cl.Noise != active || len(cl.Clusters) == 0 {
		t.Errorf("кластеры: %d в %d кластерах, вне — %d, всего %d из %d", inClusters, len(cl.Clusters), cl.Noise, cl.Total, active)
	}
	// Больший радиус — не больше кластеров вне; только склады — не больше объектов.
	wide, err := svc.Clusters(ctx, ClusterParams{EpsM: 2000, MinPoints: 3})
	if err != nil || wide.Noise > cl.Noise {
		t.Errorf("радиус 2 км: %+v %v", wide, err)
	}
	if only, err := svc.Clusters(ctx, ClusterParams{EpsM: 400, MinPoints: 3, Types: []string{"warehouse"}}); err != nil || only.Total > active {
		t.Errorf("склады: %v", err)
	}
	if _, err := svc.Clusters(ctx, ClusterParams{EpsM: 10, MinPoints: 3}); !errors.Is(err, ErrIndexConfig) {
		t.Errorf("радиус 10 м: %v", err)
	}
}

package territory

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/facility"
	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

func TestListAndProfile(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	svc := New(pool, facility.New(pool))

	// Население района из Росстата; у округа — сумма районов.
	var district, okrug uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT d.id, d.parent_id FROM territory.admin_area d
		JOIN infra.facility f ON f.district_id = d.id WHERE d.kind = 'district' AND d.parent_id IS NOT NULL LIMIT 1`).
		Scan(&district, &okrug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO territory.population (area_id, area_name, year, value, kind, source_code)
		VALUES ($1, 'район', 2024, 90000, 'january1', 'rosstat_population'),
		       ($1, 'район', 2025, 91000, 'january1', 'rosstat_population')`, district); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var d, o *Row
	for i := range list {
		switch list[i].ID {
		case district:
			d = &list[i]
		case okrug:
			o = &list[i]
		}
	}
	if d == nil || o == nil {
		t.Fatalf("нет района или округа в списке из %d", len(list))
	}
	if d.Population == nil || *d.Population != 91000 || *d.PopulationYear != 2025 || d.Density() == nil {
		t.Errorf("население района %+v", d)
	}
	if o.Population == nil || *o.Population != 91000 || o.Facilities < d.Facilities || d.Facilities == 0 {
		t.Errorf("округ %+v, район %+v", o, d)
	}
	if only, err := svc.List(ctx, "okrug"); err != nil || len(only) == 0 || only[0].Kind != "okrug" {
		t.Errorf("округа: %v", err)
	}

	p, err := svc.Get(ctx, okrug)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Children) == 0 || len(p.Population) != 2 || p.Summary == nil || p.Summary.Facilities == 0 ||
		len(p.Geometry) == 0 || p.BBox[0] >= p.BBox[2] {
		t.Errorf("профиль округа: дети %d, ряд %v, сводка %+v", len(p.Children), p.Population, p.Summary)
	}
	if _, err := svc.Get(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("нет территории: %v", err)
	}
}

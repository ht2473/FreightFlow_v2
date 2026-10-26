package scenario

import (
	"errors"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/routing"
)

func TestResilience(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	client, err := jobs.NewInserter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	// Участок у точки подменного перекрытия: объекты в 1 км от неё получают +5 мин.
	var seg uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM network.road_segment
		ORDER BY geom <-> ST_SetSRID(ST_MakePoint($1, $2), 4326) LIMIT 1`, fakeExclusion.Lon, fakeExclusion.Lat).Scan(&seg); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouting{
		built: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), target: routing.Point{Lon: 37.84, Lat: 55.74},
		top: []routing.CriticalElementRef{
			{VersionID: 7, ID: 3, Kind: "bridge", Name: "Тестовый мост", Segments: []uuid.UUID{seg}, Rank: 1, DamageMin: 10},
			{VersionID: 7, ID: 4, Kind: "segment", Name: "Без ущерба", Segments: []uuid.UUID{seg}, Rank: 2, DamageMin: 0},
		},
	}
	svc := New(pool, client, fr)

	st, err := svc.RunResilience(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped || st.Elements != 1 || st.Requests == 0 {
		t.Fatalf("stats = %+v", st)
	}
	last, running, rows, err := svc.Resilience(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last == nil || last.Status != "done" || running != nil || len(rows) != 1 {
		t.Fatalf("run = %+v, running %+v, rows %d", last, running, len(rows))
	}
	var near int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM infra.facility WHERE status = 'active'
		AND ST_DWithin(point::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 900)`,
		fakeExclusion.Lon, fakeExclusion.Lat).Scan(&near); err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.Rank != 1 || r.ElementID != 3 || r.Candidates == 0 || r.Affected < near || r.Lost != 0 {
		t.Errorf("row = %+v, want ≥ %d affected", r, near)
	}
	// Цели только МКАД (ТТК и ж/д в подмене нет): ущерб — 5 мин на затронутый объект.
	if len(r.ByTarget) != 1 || r.ByTarget[0].Kind != "mkad" || math.Abs(r.Damage-5*float64(r.Affected)) > 0.5 {
		t.Errorf("damage = %v by %+v, affected %d", r.Damage, r.ByTarget, r.Affected)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM ingest.data_version WHERE domain = 'resilience'`).Scan(&version); err != nil || version != 1 {
		t.Errorf("resilience version = %d, %v", version, err)
	}
	var tile []byte
	if err := pool.QueryRow(ctx, `SELECT tiles.resilience(9, 309, 160)`).Scan(&tile); err != nil {
		t.Fatal(err)
	}

	// Граф и анализ не менялись — пакет не пересчитывается; с force — новый, прежний удаляется.
	if st, err := svc.RunResilience(ctx, false); err != nil || !st.Skipped {
		t.Errorf("unchanged: %+v, %v", st, err)
	}
	st2, err := svc.RunResilience(ctx, true)
	if err != nil || st2.Skipped || st2.RunID == st.RunID {
		t.Fatalf("forced: %+v, %v", st2, err)
	}
	var runs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scenario.resilience_run`).Scan(&runs); err != nil || runs != 1 {
		t.Errorf("runs = %d, %v", runs, err)
	}

	fr.top = nil
	if _, err := svc.RunResilience(ctx, true); !errors.Is(err, ErrNoAnalysis) {
		t.Errorf("no analysis: %v", err)
	}
}

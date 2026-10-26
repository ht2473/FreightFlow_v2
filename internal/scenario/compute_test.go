package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/routing"
)

// fakeRouting — предсказуемая маршрутизация: время до цели — 10 мин на километр по прямой;
// перекрытие участка добавляет 5 мин объектам в 1 км от него; область отрезает объекты внутри.
type fakeRouting struct {
	built   time.Time
	target  routing.Point
	calls   atomic.Int64
	isoReqs atomic.Int64
	// top — рейтинг критичности для пакета устойчивости.
	top []routing.CriticalElementRef
}

var fakeExclusion = routing.Point{Lon: 37.72, Lat: 55.735}

func (f *fakeRouting) ProfileByCode(_ context.Context, code string) (routing.Profile, error) {
	return routing.Profile{Code: code, Name: code, GrossT: 12}, nil
}
func (f *fakeRouting) Rules(context.Context) ([]routing.ZoneRule, error) { return nil, nil }
func (f *fakeRouting) ZoneRing(context.Context, string) ([]routing.Ring, error) {
	return nil, nil
}

func (f *fakeRouting) BlockedPoints(context.Context, []routing.Point, []routing.ZoneDecision) ([]routing.BlockedPoint, error) {
	return nil, nil
}

func km(a, b routing.Point) float64 {
	dy := (a.Lat - b.Lat) * 111.32
	dx := (a.Lon - b.Lon) * 111.32 * math.Cos(a.Lat*math.Pi/180)
	return math.Hypot(dx, dy)
}

func inside(p routing.Point, r routing.Ring) bool {
	lo, hi := r[0], r[0]
	for _, v := range r {
		lo = [2]float64{min(lo[0], v[0]), min(lo[1], v[1])}
		hi = [2]float64{max(hi[0], v[0]), max(hi[1], v[1])}
	}
	return p.Lon >= lo[0] && p.Lon <= hi[0] && p.Lat >= lo[1] && p.Lat <= hi[1]
}

func (f *fakeRouting) Nearest(_ context.Context, in routing.NearestInput) ([]float64, []float64, error) {
	f.calls.Add(1)
	mins, kms := make([]float64, len(in.Sources)), make([]float64, len(in.Sources))
	for i, s := range in.Sources {
		d := km(s, in.Targets[0])
		mins[i], kms[i] = 10*d, d
		for _, r := range in.Exclude.Polygons {
			if inside(s, r) {
				mins[i], kms[i] = -1, -1
			}
		}
		if len(in.Exclude.Locations) > 0 && mins[i] >= 0 && km(s, fakeExclusion) < 1 {
			mins[i] += 5
		}
	}
	if in.Progress != nil {
		in.Progress(1, 1)
	}
	return mins, kms, nil
}

func (f *fakeRouting) IsochroneWith(_ context.Context, o routing.Point, _ routing.Profile, minutes []int, _ time.Time,
	ex routing.Exclusions,
) (json.RawMessage, error) {
	f.isoReqs.Add(1)
	size := 0.01
	if len(ex.Locations) > 0 {
		size = 0.005
	}
	var feats []any
	for _, m := range minutes {
		d := size * float64(m) / 15
		ring := [][2]float64{{o.Lon - d, o.Lat - d}, {o.Lon + d, o.Lat - d}, {o.Lon + d, o.Lat + d}, {o.Lon - d, o.Lat + d}, {o.Lon - d, o.Lat - d}}
		feats = append(feats, map[string]any{
			"type": "Feature", "properties": map[string]any{"contour": m},
			"geometry": map[string]any{"type": "Polygon", "coordinates": [][][2]float64{ring}},
		})
	}
	return json.Marshal(map[string]any{"type": "FeatureCollection", "features": feats})
}

func (f *fakeRouting) SegmentExclusions(_ context.Context, _ string, segs []uuid.UUID) ([]routing.ExcludePoint, error) {
	out := make([]routing.ExcludePoint, len(segs))
	for i := range out {
		out[i] = routing.ExcludePoint{Point: fakeExclusion, Heading: 90}
	}
	return out, nil
}

func (f *fakeRouting) Element(_ context.Context, id int) (routing.CriticalElementRef, error) {
	return routing.CriticalElementRef{ID: id, Kind: "bridge", Name: "Тестовый мост"}, nil
}

func (f *fakeRouting) TopElements(context.Context, int) ([]routing.CriticalElementRef, error) {
	return f.top, nil
}

func (f *fakeRouting) Targets(context.Context) (map[string][]routing.Point, error) {
	return map[string][]routing.Point{"mkad": {f.target}}, nil
}

func (f *fakeRouting) GraphBuiltAt(context.Context) (time.Time, error) { return f.built, nil }

func TestScenarioRun(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name) VALUES ($1, 'scenario@example.com', 'Сценарий')`, user); err != nil {
		t.Fatal(err)
	}
	client, err := jobs.NewInserter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouting{built: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), target: routing.Point{Lon: 37.84, Lat: 55.74}}
	svc := New(pool, client, fr)

	var facilities int
	var segment uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM infra.facility WHERE status = 'active'`).Scan(&facilities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM network.road_segment ORDER BY id LIMIT 1`).Scan(&segment); err != nil {
		t.Fatal(err)
	}
	var iso uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM infra.facility WHERE status = 'active' ORDER BY id LIMIT 1`).Scan(&iso); err != nil {
		t.Fatal(err)
	}
	if facilities == 0 {
		t.Fatal("fragment has no facilities")
	}

	sc, err := svc.Create(ctx, user, "Перекрытие участка", "", Params{
		Profile:             "truck_12",
		Closures:            []Closure{{Kind: ClosureSegments, Segments: []uuid.UUID{segment}}},
		NewFacility:         &NewFacility{Name: "Склад", Lon: 37.73, Lat: 55.735},
		IsochroneFacilities: []uuid.UUID{iso},
		IsochroneMinutes:    []int{15, 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Status != StatusQueued {
		t.Fatalf("status = %s", sc.Status)
	}
	if err := svc.run(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, user, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.Summary == nil || got.GraphBuiltAt == nil || !got.GraphBuiltAt.Equal(fr.built) {
		t.Fatalf("scenario = %+v, error %v", got, got.Error)
	}
	sum := got.Summary
	if sum.Facilities != facilities || sum.BaseReused || len(sum.Targets) != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	// Ожидание по той же модели, что у подменной маршрутизации.
	rows, err := pool.Query(ctx, `SELECT ST_X(point), ST_Y(point) FROM infra.facility WHERE status = 'active'`)
	if err != nil {
		t.Fatal(err)
	}
	affected := 0
	for rows.Next() {
		var p routing.Point
		if err := rows.Scan(&p.Lon, &p.Lat); err != nil {
			t.Fatal(err)
		}
		if km(p, fakeExclusion) < 1 {
			affected++
		}
	}
	rows.Close()
	ts := sum.Targets[0]
	if ts.Affected != affected || ts.Lost != 0 || math.Abs(ts.DelayMin-5*float64(affected)) > 0.01 {
		t.Errorf("target summary = %+v, want %d affected by 5 min", ts, affected)
	}
	if len(sum.Closures) != 1 || sum.Closures[0].Segments != 1 || sum.Closures[0].Excluded != 1 {
		t.Errorf("closures = %+v", sum.Closures)
	}
	if len(sum.NewFacility) != 1 || sum.NewFacility[0].BaseMin == nil || *sum.NewFacility[0].ScenarioMin-*sum.NewFacility[0].BaseMin != 5 {
		t.Errorf("new facility = %+v", sum.NewFacility)
	}

	var deltas int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scenario.facility_delta WHERE scenario_id = $1`, sc.ID).Scan(&deltas); err != nil {
		t.Fatal(err)
	}
	if deltas != facilities+1 {
		t.Errorf("deltas = %d, want %d", deltas, facilities+1)
	}
	changed, err := svc.Facilities(ctx, user, sc.ID, "mkad", false, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != affected+1 { // и новый объект
		t.Errorf("changed facilities = %d, want %d", len(changed), affected+1)
	}
	if affected > 0 {
		districts, err := svc.Districts(ctx, user, sc.ID, "mkad")
		if err != nil || len(districts) == 0 || districts[0].Affected == 0 {
			t.Errorf("districts = %+v, %v", districts, err)
		}
	}

	// Две точки × два варианта, площадь сценария меньше базы.
	if len(sum.Isochrones) != 4 || fr.isoReqs.Load() != 4 {
		t.Fatalf("isochrones = %+v (requests %d)", sum.Isochrones, fr.isoReqs.Load())
	}
	for _, i := range sum.Isochrones {
		if !(i.ScenarioKm2 < i.BaseKm2) || i.Name == "" {
			t.Errorf("isochrone %+v: scenario area must be smaller", i)
		}
	}
	gj, err := svc.Isochrones(ctx, user, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fc struct{ Features []json.RawMessage }
	if err := json.Unmarshal(gj, &fc); err != nil || len(fc.Features) != 8 {
		t.Errorf("geojson features = %d, %v", len(fc.Features), err)
	}

	cl, err := svc.Closures(ctx, user, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cl, &fc); err != nil || len(fc.Features) != 1 {
		t.Errorf("closure features = %d, %v", len(fc.Features), err)
	}

	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notify.outbox WHERE type = 'scenario.computed' AND payload->>'id' = $1`,
		sc.ID.String()).Scan(&events); err != nil || events != 1 {
		t.Errorf("scenario.computed events = %d, %v", events, err)
	}

	// Чужой пользователь сценария не видит.
	if _, err := svc.Get(ctx, uuid.New(), sc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign get: %v", err)
	}

	// База без правил зон из показателей доступности того же графа: меньше запросов матриц.
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.accessibility (facility_id, profile_code, target_kind, minutes, km, graph_built_at)
		SELECT id, 'truck_12', 'mkad', 1, 1, $1 FROM infra.facility WHERE status = 'active'`, fr.built); err != nil {
		t.Fatal(err)
	}
	before := fr.calls.Load()
	if _, err := svc.Rerun(ctx, user, sc.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.run(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	again, _ := svc.Get(ctx, user, sc.ID)
	// База — показатели (1 мин), сценарий — подменная модель: время растёт у всех объектов.
	if !again.Summary.BaseReused || again.Summary.Targets[0].Affected != facilities {
		t.Errorf("reused summary = %+v", again.Summary.Targets)
	}
	// Сценарий и отдельно новый объект для базы.
	if n := fr.calls.Load() - before; n != 2 {
		t.Errorf("matrix calls = %d, want 2", n)
	}

	if err := svc.Delete(ctx, user, sc.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scenario.facility_delta WHERE scenario_id = $1`, sc.ID).Scan(&deltas); err != nil || deltas != 0 {
		t.Errorf("deltas after delete = %d, %v", deltas, err)
	}
}

func TestScenarioFailure(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name) VALUES ($1, 'fail@example.com', 'Ошибка')`, user); err != nil {
		t.Fatal(err)
	}
	client, err := jobs.NewInserter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	fr := &failingRouting{fakeRouting{built: time.Now()}}
	svc := New(pool, client, fr)
	sc, err := svc.Create(ctx, user, "Без службы", "", Params{Profile: "truck_12", NewFacility: &NewFacility{Lon: 37.6, Lat: 55.7}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.run(ctx, sc.ID); err == nil {
		t.Fatal("run must fail")
	}
	got, _ := svc.Get(ctx, user, sc.ID)
	if got.Status != StatusFailed || got.Error == nil || *got.Error != "Служба маршрутизации недоступна" {
		t.Errorf("failed scenario = %s, %v", got.Status, got.Error)
	}
}

type failingRouting struct{ fakeRouting }

func (f *failingRouting) GraphBuiltAt(context.Context) (time.Time, error) {
	return time.Time{}, routing.ErrUnavailable
}

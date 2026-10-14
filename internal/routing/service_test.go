package routing

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"freightflow/internal/platform/dbtest"
)

func TestMatrixClosedZone(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	// Зона МКАД — квадрат вокруг первой точки; вторая точка и цели — снаружи.
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO territory.ring_zone (code, name, ord, line, zone, length_km, source_code)
		VALUES ('mkad', 'МКАД', 3, ST_Multi(ST_Boundary(ST_MakeEnvelope(37.5, 55.6, 37.7, 55.8, 4326))),
		        ST_Multi(ST_MakeEnvelope(37.5, 55.6, 37.7, 55.8, 4326)), 80, 'test')`); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Sources, Targets []Point
	}
	var raw map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_ = json.Unmarshal(raw["sources"], &body.Sources)
		_ = json.Unmarshal(raw["targets"], &body.Targets)
		rows := make([][]map[string]any, len(body.Sources))
		for i := range rows {
			for j := range body.Targets {
				rows[i] = append(rows[i], map[string]any{"time": 100*(i+1) + j, "distance": 1.5})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sources_to_targets": rows})
	}))
	defer srv.Close()

	svc := NewService(pool, NewClient(srv.URL))
	truck := Profile{GrossT: 20, PayloadT: 11, AxleLoadT: 10, HeightM: 4, WidthM: 2.55, LengthM: 10, EcoClass: 5}
	// Будний день, 10:00 по Москве: грузовику 20 т без пропуска внутрь МКАД нельзя.
	depart := time.Date(2026, 10, 14, 10, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	out, err := svc.Matrix(t.Context(), MatrixInput{
		Sources: []Point{{Lon: 37.6, Lat: 55.7}, {Lon: 37.9, Lat: 55.9}},
		Targets: []Point{{Lon: 38.0, Lat: 55.5}, {Lon: 37.2, Lat: 55.9}, {Lon: 37.65, Lat: 55.75}},
		Profile: truck, Depart: depart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.BlockedSources) != 1 || out.BlockedSources[0].Index != 0 || out.BlockedSources[0].Zone != "mkad" ||
		len(out.BlockedTargets) != 1 || out.BlockedTargets[0].Index != 2 {
		t.Fatalf("blocked: %+v, %+v", out.BlockedSources, out.BlockedTargets)
	}
	// В службу ушли только открытые точки, зона — исключением.
	if len(body.Sources) != 1 || len(body.Targets) != 2 || len(raw["exclude_polygons"]) == 0 {
		t.Fatalf("request: %+v, exclude %s", body, raw["exclude_polygons"])
	}
	// Ячейки — на местах исходных точек; у закрытых точек — недостижимы.
	if out.Cells[0][0].Reachable || !out.Cells[1][0].Reachable || out.Cells[1][0].Time != 100 ||
		out.Cells[1][1].Time != 101 || out.Cells[1][2].Reachable {
		t.Fatalf("cells: %+v", out.Cells)
	}

	_, err = svc.Matrix(t.Context(), MatrixInput{Sources: make([]Point, 51), Targets: make([]Point, 50), Profile: truck})
	if !errors.Is(err, ErrMatrixTooLarge) {
		t.Errorf("too large: %v", err)
	}
}

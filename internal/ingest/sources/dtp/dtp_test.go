package dtp

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
)

// testdata/moskva.geojson — пять записей выгрузки «Карты ДТП» по Москве (сведения об участниках
// сокращены до роли); у одной нет координат.
func readFixture(t *testing.T) []Incident {
	t.Helper()
	f, err := os.Open("testdata/moskva.geojson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Incident
	skipped, err := EachIncident(f, func(in Incident) error {
		out = append(out, in)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	return out
}

func TestEachIncident(t *testing.T) {
	t.Parallel()
	got := readFixture(t)
	if len(got) != 4 {
		t.Fatalf("incidents = %d, want 4", len(got))
	}
	first := got[0]
	if first.ID != 6 || first.Severity != "С погибшими" || first.Dead != 1 || first.Lon != 37.608056 || first.Lat != 55.689444 {
		t.Errorf("first = %+v", first)
	}
	if len(got[1].Vehicles) != 3 || got[1].Vehicles[2] != "Самосвалы" {
		t.Errorf("vehicles = %v", got[1].Vehicles)
	}
}

func TestEachIncidentRejectsNonCollection(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{`[]`, `{"type":"FeatureCollection"}`, `{"features":{}}`} {
		f, err := os.CreateTemp(t.TempDir(), "bad")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(bad)
		_, _ = f.Seek(0, 0)
		if _, err := EachIncident(f, func(Incident) error { return nil }); err == nil {
			t.Errorf("%s accepted", bad)
		}
		f.Close()
	}
}

func TestIncidentsTransform(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	script, err := etl.FS.ReadFile("dtp/incidents.sql")
	if err != nil {
		t.Fatal(err)
	}
	recs := readFixture(t)
	err = db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `CREATE TEMP TABLE dtp_stage (
			source_id bigint, datetime text, category text, severity text, dead int, injured int, participants int,
			light text, weather text[], road_conditions text[], nearby text[], address text, district text,
			vehicles text[], lon float8, lat float8) ON COMMIT DROP`); err != nil {
			return err
		}
		for _, in := range recs {
			if _, err := tx.Exec(t.Context(), `INSERT INTO dtp_stage VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
				in.ID, in.Datetime, in.Category, in.Severity, in.Dead, in.Injured, in.Participants, in.Light, in.Weather,
				in.RoadConditions, in.Nearby, in.Address, in.District, in.Vehicles, in.Lon, in.Lat); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(t.Context(), `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true)`,
			Code, uuid.NewString()); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), string(script))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		severity       string
		freight, heavy bool
		at             time.Time
	}
	want := map[int64]row{
		6:    {"fatal", false, false, time.Date(2014, 12, 31, 21, 23, 0, 0, time.UTC)},
		2217: {"light", true, true, time.Date(2015, 1, 6, 8, 30, 0, 0, time.UTC)},
		104:  {"light", true, false, time.Date(2015, 1, 1, 1, 0, 0, 0, time.UTC)},
		22:   {"severe", false, false, time.Date(2014, 12, 31, 22, 0, 0, 0, time.UTC)},
	}
	rows, err := pool.Query(t.Context(), `SELECT source_id, severity, freight, heavy_freight, occurred_at FROM operations.incident`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id int64
		var r row
		if err := rows.Scan(&id, &r.severity, &r.freight, &r.heavy, &r.at); err != nil {
			t.Fatal(err)
		}
		if w, ok := want[id]; !ok || w.severity != r.severity || w.freight != r.freight || w.heavy != r.heavy || !w.at.Equal(r.at) {
			t.Errorf("incident %d = %+v, want %+v", id, r, w)
		}
		n++
	}
	if n != len(want) {
		t.Errorf("incidents = %d, want %d", n, len(want))
	}
}

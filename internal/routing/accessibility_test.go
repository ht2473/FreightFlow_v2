package routing

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/etl"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/dbtest"
)

// loadDistrict переносит фрагмент OSM Нижегородского района (../ingest/sources/osm/testdata) в
// домен: объекты реестра, сеть, кольца.
func loadDistrict(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	f, err := os.Open("../ingest/sources/osm/testdata/stage.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(stage)); err != nil {
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

// fakeValhalla отвечает на /status и матрицы: время — 60 с × (номер цели + 1), первый объект
// каждой порции недостижим.
func fakeValhalla(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_, _ = io.WriteString(w, `{"version":"3.9.1","tileset_last_modified":1791904532}`)
			return
		}
		var req struct {
			Sources []struct {
				Radius int `json:"radius"`
			} `json:"sources"`
			Targets []json.RawMessage `json:"targets"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Sources) == 0 || req.Sources[0].Radius != facilityRadius {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rows := make([][]map[string]any, len(req.Sources))
		for i := range req.Sources {
			for j := range req.Targets {
				cell := map[string]any{"time": nil, "distance": nil}
				if i > 0 {
					cell = map[string]any{"time": 60 * (j + 1), "distance": float64(j+1) / 2}
				}
				rows[i] = append(rows[i], cell)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sources_to_targets": rows})
	}))
}

func TestRefreshAccessibility(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	loadDistrict(t, pool)
	srv := fakeValhalla(t)
	defer srv.Close()

	st, err := NewService(pool, NewClient(srv.URL)).RefreshAccessibility(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var facilities, profiles, kinds int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM infra.facility WHERE status = 'active'),
		(SELECT count(*) FROM ref.vehicle_profile), (SELECT count(DISTINCT kind) FROM analytics.accessibility_target)`).
		Scan(&facilities, &profiles, &kinds); err != nil {
		t.Fatal(err)
	}
	if facilities == 0 || st.Facilities != facilities || st.Profiles != profiles || kinds == 0 {
		t.Fatalf("stats %+v; facilities %d, profiles %d, target kinds %d", st, facilities, profiles, kinds)
	}
	var rows, reachable, nearest int
	if err := pool.QueryRow(t.Context(), `SELECT count(*), count(minutes),
		count(*) FILTER (WHERE minutes = 1 AND km = 0.5) FROM analytics.accessibility`).Scan(&rows, &reachable, &nearest); err != nil {
		t.Fatal(err)
	}
	// Строка на объект, профиль и вид цели; у достижимых — ближайшая цель (первая: 1 мин, 0,5 км).
	if rows != facilities*profiles*kinds || reachable != rows-st.Unreachable || nearest != reachable {
		t.Errorf("rows %d (want %d), reachable %d, nearest %d, unreachable %d", rows, facilities*profiles*kinds,
			reachable, nearest, st.Unreachable)
	}
	// Тайлы объектов перестраиваются: версия домена доступности опубликована.
	var v int64
	if err := pool.QueryRow(t.Context(), `SELECT version FROM ingest.data_version WHERE domain = 'accessibility'`).Scan(&v); err != nil || v != 1 {
		t.Errorf("accessibility version %d, %v", v, err)
	}
}

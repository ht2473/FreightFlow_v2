package tiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paulmach/orb/encoding/mvt"

	"freightflow/internal/identity"
	"freightflow/internal/platform/dbtest"
)

type fixedVersions map[string]int64

func (v fixedVersions) DataVersions(context.Context) (map[string]int64, error) { return v, nil }

// Тайл z14 с Китай-городом (14/9904/5121: 37,617–37,639° в. д., 55,752–55,764° с. ш.). Идентификатор объекта в тайле —
// osm_id × 4 + вид (W = 1): 101 × 4 + 1 = 405.
const tz, tx, ty = 14, 9904, 5121

// seed — объект, участок с ограничениями и АЗС в пределах тайла (координаты условные,
// значения свойств — по смыслу полей).
func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO infra.facility (id, type_code, name, point, building_area_m2, buildings, confidence, osm_type, osm_id, source_code)
		VALUES (uuidv7(), 'warehouse', 'Склад на Варварке', ST_SetSRID(ST_MakePoint(37.6290, 55.7560), 4326), 4200, 2, 0.75, 'W', 101, 'osm'),
		       (uuidv7(), 'terminal', 'Прежний терминал', ST_SetSRID(ST_MakePoint(37.6295, 55.7562), 4326), NULL, 0, 0.5, 'N', 102, 'osm');
		UPDATE infra.facility SET status = 'missing' WHERE osm_id = 102;

		INSERT INTO network.road_segment (id, osm_id, class, name, length_m, geom, source_code)
		VALUES (uuidv7(), 201, 'primary', 'улица Варварка', 420,
		        ST_SetSRID(ST_MakeLine(ST_MakePoint(37.6260, 55.7555), ST_MakePoint(37.6320, 55.7565)), 4326), 'osm');
		INSERT INTO network.restriction (id, segment_id, kind, value, unit, condition, raw, origin, source_code)
		SELECT uuidv7(), id, 'maxweight', 3.5, 't', NULL, '3.5', 'osm', 'osm' FROM network.road_segment WHERE osm_id = 201;
		INSERT INTO network.restriction (id, segment_id, kind, condition, raw, origin, source_code)
		SELECT uuidv7(), id, 'hgv_ban', 'Mo-Fr 07:00-22:00', 'no @ (Mo-Fr 07:00-22:00)', 'osm', 'osm' FROM network.road_segment WHERE osm_id = 201;

		INSERT INTO infra.poi (id, kind, name, point, osm_type, osm_id, source_code)
		VALUES (uuidv7(), 'fuel', 'АЗС', ST_SetSRID(ST_MakePoint(37.6300, 55.7550), 4326), 'N', 301, 'osm');`)
	if err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, data []byte) mvt.Layers {
	t.Helper()
	layers, err := mvt.UnmarshalGzipped(data)
	if err != nil {
		t.Fatal(err)
	}
	return layers
}

func TestDataTiles(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	seed(t, pool)
	svc := NewService(pool, fixedVersions{"facility": 3, "network": 5, "infra": 2}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	l, _ := layerByCode("facilities")
	data, version, err := svc.Tile(t.Context(), l, tz, tx, ty)
	if err != nil {
		t.Fatal(err)
	}
	if version != "3" {
		t.Fatalf("version %q", version)
	}
	layers := decode(t, data)
	if len(layers) != 1 || layers[0].Name != "facilities" || len(layers[0].Features) != 1 {
		t.Fatalf("facilities: %d layers", len(layers))
	}
	f := layers[0].Features[0]
	if f.Properties["name"] != "Склад на Варварке" || f.Properties["type"] != "warehouse" ||
		f.Properties["area"] != 4200.0 || f.Properties["confidence"] != 0.75 || fmt.Sprint(f.ID) != "405" {
		t.Fatalf("facility feature: id %v, %v", f.ID, f.Properties)
	}

	// Ограничения участка сводятся в одну линию: запрет по времени не перекрывает предел массы.
	l, _ = layerByCode("restrictions")
	data, _, err = svc.Tile(t.Context(), l, tz, tx, ty)
	if err != nil {
		t.Fatal(err)
	}
	r := decode(t, data)[0].Features[0].Properties
	if r["severity"] != "limit" || r["maxweight"] != 3.5 || r["conditional"] != false ||
		r["conditions"] != "Mo-Fr 07:00-22:00" || r["kinds"] != "hgv_ban,maxweight" {
		t.Fatalf("restriction: %v", r)
	}

	l, _ = layerByCode("fuel_stations")
	data, _, err = svc.Tile(t.Context(), l, tz, tx, ty)
	if err != nil {
		t.Fatal(err)
	}
	if p := decode(t, data)[0].Features[0].Properties; p["kind"] != "fuel" {
		t.Fatalf("fuel: %v", p)
	}

	// Пустой тайл.
	l, _ = layerByCode("facilities")
	if data, _, err = svc.Tile(t.Context(), l, 10, 0, 0); err != nil || len(data) != 0 {
		t.Fatalf("empty tile: %d bytes, %v", len(data), err)
	}
}

// Все функции слоёв реестра существуют и строят тайл (в том числе на мелком масштабе).
func TestRegistryFunctions(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	seed(t, pool)
	svc := NewService(pool, fixedVersions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, l := range Registry {
		for _, z := range []uint8{uint8(l.MinZoom), uint8(l.MaxZoom)} {
			x, y := uint32(tx)>>(tz-z), uint32(ty)>>(tz-z)
			if _, _, err := svc.Tile(t.Context(), l, z, x, y); err != nil {
				t.Errorf("%s z%d: %v", l.Code, z, err)
			}
		}
	}
}

type fakeIdentity struct{ p identity.Principal }

func (f fakeIdentity) Principal(context.Context, uuid.UUID) (identity.Principal, error) {
	return f.p, nil
}

func (fakeIdentity) PasswordChangedAt(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

func (f fakeIdentity) AuthenticateAPIToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "good" {
		return identity.Principal{}, errors.New("bad token")
	}
	return f.p, nil
}

type noSession struct{}

func (noSession) UserID(context.Context) (uuid.UUID, time.Time, bool) {
	return uuid.Nil, time.Time{}, false
}

func TestDataTileAccess(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	seed(t, pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(pool, fixedVersions{"facility": 3}, log)
	viewer := identity.Principal{Permissions: []identity.Permission{identity.PermMapView}}
	h := New(Deps{Basemap: NewBasemap(t.TempDir(), log), Data: svc, Auth: NewAuth(fakeIdentity{p: viewer}, noSession{}), Log: log})
	blind := New(Deps{Basemap: NewBasemap(t.TempDir(), log), Data: svc, Auth: NewAuth(fakeIdentity{}, noSession{}), Log: log})

	call := func(h http.Handler, url, token, etag string) *http.Response {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Result()
	}
	tile := "/tiles/facilities/14/9904/5121.mvt?v=3"
	cases := []struct {
		name   string
		h      http.Handler
		url    string
		token  string
		etag   string
		status int
	}{
		{"anonymous", h, tile, "", "", http.StatusUnauthorized},
		{"bad token", h, tile, "bad", "", http.StatusUnauthorized},
		{"no permission", blind, tile, "good", "", http.StatusForbidden},
		{"tile", h, tile, "good", "", http.StatusOK},
		{"not modified", h, tile, "good", `"facilities-3"`, http.StatusNotModified},
		{"below min zoom", h, "/tiles/facilities/6/38/20.mvt", "good", "", http.StatusNoContent},
		{"unknown layer", h, "/tiles/nope/14/9904/5121.mvt", "good", "", http.StatusNotFound},
		{"empty", h, "/tiles/facilities/10/0/0.mvt", "good", "", http.StatusNoContent},
	}
	for _, c := range cases {
		resp := call(c.h, c.url, c.token, c.etag)
		if resp.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d", c.name, resp.StatusCode, c.status)
		}
	}
	resp := call(h, tile, "good", "")
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Cache-Control") != "private, max-age=86400" {
		t.Fatalf("headers: %v", resp.Header)
	}
}

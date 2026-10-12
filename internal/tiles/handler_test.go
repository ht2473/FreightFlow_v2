package tiles

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"freightflow/internal/ingest/sources/basemap"
)

// newBasemapDir — каталог данных с фрагментом подложки (testdata пакета pmtiles).
func newBasemapDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "platform", "pmtiles", "testdata", "kremlin.pmtiles"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "raw", "basemap"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "raw", "basemap", "a.pmtiles"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	writePointer(t, dir, basemap.Ref{Path: "raw/basemap/a.pmtiles", Version: "v1"})
	return dir
}

func writePointer(t *testing.T, dir string, ref basemap.Ref) {
	t.Helper()
	b, _ := json.Marshal(ref)
	if err := os.MkdirAll(filepath.Join(dir, "basemap"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "basemap", "current.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, h http.Handler, url string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil))
	return rec.Result()
}

func TestBasemapRoutes(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bm := NewBasemap(newBasemapDir(t), log)
	defer bm.Close()
	h := New(Deps{Basemap: bm, Log: log})

	resp := get(t, h, "http://example.test/map/basemap.json")
	var tj struct {
		Tiles        []string          `json:"tiles"`
		MinZoom      int               `json:"minzoom"`
		MaxZoom      int               `json:"maxzoom"`
		VectorLayers []json.RawMessage `json:"vector_layers"`
		Attribution  string            `json:"attribution"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tj); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("tilejson: %d %v", resp.StatusCode, err)
	}
	if len(tj.Tiles) != 1 || tj.Tiles[0] != "http://example.test/map/basemap/v1/{z}/{x}/{y}.mvt" ||
		tj.MinZoom != 12 || tj.MaxZoom != 14 || len(tj.VectorLayers) == 0 || tj.Attribution == "" {
		t.Fatalf("tilejson: %+v", tj)
	}

	cases := []struct {
		name, url string
		status    int
		header    map[string]string
	}{
		{"tile", "/map/basemap/v1/12/2475/1280.mvt", http.StatusOK, map[string]string{
			"Content-Encoding": "gzip", "Content-Type": "application/vnd.mapbox-vector-tile",
			"Cache-Control": "public, max-age=31536000, immutable",
		}},
		{"stale version", "/map/basemap/old/12/2475/1280.mvt", http.StatusOK, map[string]string{"Cache-Control": "no-cache"}},
		{"empty tile", "/map/basemap/v1/12/0/0.mvt", http.StatusNoContent, nil},
		{"bad coords", "/map/basemap/v1/2/9/0.mvt", http.StatusNotFound, nil},
		{"no extension", "/map/basemap/v1/12/2475/1280", http.StatusNotFound, nil},
		{"glyphs", "/map/fonts/Noto%20Sans%20Regular/1024-1279.pbf", http.StatusOK, map[string]string{"Content-Type": "application/x-protobuf"}},
		{"glyph fallback stack", "/map/fonts/Unknown,Noto%20Sans%20Medium/0-255.pbf", http.StatusOK, nil},
		{"missing range is empty", "/map/fonts/Noto%20Sans%20Regular/65280-65535.pbf", http.StatusOK, nil},
		{"sprite", "/map/sprites/light@2x.png", http.StatusOK, map[string]string{"Content-Type": "image/png"}},
		{"sprite index", "/map/sprites/dark.json", http.StatusOK, map[string]string{"Content-Type": "application/json"}},
		{"no license", "/map/sprites/LICENSE.md", http.StatusNotFound, nil},
	}
	for _, c := range cases {
		resp := get(t, h, c.url)
		if resp.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d", c.name, resp.StatusCode, c.status)
			continue
		}
		for k, v := range c.header {
			if got := resp.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got, v)
			}
		}
	}
	// Глифы кириллицы — непустой диапазон.
	if b, _ := io.ReadAll(get(t, h, "/map/fonts/Noto%20Sans%20Regular/1024-1279.pbf").Body); len(b) < 1000 {
		t.Fatalf("cyrillic glyphs: %d bytes", len(b))
	}
}

func TestBasemapMissing(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(Deps{Basemap: NewBasemap(t.TempDir(), log), Log: log})
	if resp := get(t, h, "/map/basemap.json"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

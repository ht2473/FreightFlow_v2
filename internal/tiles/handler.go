// Package tiles раздаёт карту: подложку из PMTiles, шрифты и значки подложки, векторные
// тайлы слоёв данных.
package tiles

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"freightflow/internal/platform/pmtiles"
)

//go:embed assets/fonts assets/sprites
var assets embed.FS

// BasemapAttribution — подпись подложки на карте.
const BasemapAttribution = `<a href="https://protomaps.com" target="_blank" rel="noopener">Protomaps</a> © <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">участники OpenStreetMap</a>`

// Deps — зависимости раздачи карты.
type Deps struct {
	Basemap *Basemap
	// Data и Auth — тайлы слоёв данных; без них раздаётся только подложка.
	Data *Service
	Auth *Auth
	Log  *slog.Logger
}

// Handler — маршруты /map/...
type Handler struct {
	d     Deps
	fonts fs.FS
	icons fs.FS
}

// New создаёт обработчик карты.
func New(d Deps) http.Handler {
	fonts, _ := fs.Sub(assets, "assets/fonts")
	icons, _ := fs.Sub(assets, "assets/sprites")
	h := &Handler{d: d, fonts: fonts, icons: icons}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /map/basemap.json", h.tileJSON)
	mux.HandleFunc("GET /map/basemap/{version}/{z}/{x}/{y}", h.basemapTile)
	mux.HandleFunc("GET /map/fonts/{stack}/{range}", h.glyphs)
	mux.HandleFunc("GET /map/sprites/{file}", h.sprite)
	if d.Data != nil && d.Auth != nil {
		mux.HandleFunc("GET /tiles/{layer}/{z}/{x}/{y}", h.dataTile)
	}
	return mux
}

// tileJSON описывает подложку (TileJSON 3.0); адрес тайлов содержит версию архива,
// поэтому тайлы кешируются как неизменяемые.
func (h *Handler) tileJSON(w http.ResponseWriter, r *http.Request) {
	arch, version, err := h.d.Basemap.Current()
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	hd := arch.Header()
	doc := map[string]any{
		"tilejson":    "3.0.0",
		"name":        "basemap",
		"scheme":      "xyz",
		"tiles":       []string{origin(r) + "/map/basemap/" + version + "/{z}/{x}/{y}.mvt"},
		"minzoom":     hd.MinZoom,
		"maxzoom":     hd.MaxZoom,
		"bounds":      hd.Bounds,
		"center":      []float64{hd.Center[0], hd.Center[1], float64(hd.CenterZoom)},
		"attribution": BasemapAttribution,
	}
	if meta, err := arch.Metadata(); err == nil {
		var m struct {
			VectorLayers json.RawMessage `json:"vector_layers"`
		}
		if json.Unmarshal(meta, &m) == nil && m.VectorLayers != nil {
			doc["vector_layers"] = m.VectorLayers
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(doc)
}

func (h *Handler) basemapTile(w http.ResponseWriter, r *http.Request) {
	z, x, y, ok := tileCoords(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	arch, version, err := h.d.Basemap.Current()
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	data, err := arch.Tile(z, x, y)
	if err != nil {
		h.d.Log.ErrorContext(r.Context(), "basemap tile", "z", z, "x", x, "y", y, "error", err)
		http.Error(w, "tile read error", http.StatusInternalServerError)
		return
	}
	if r.PathValue("version") == version {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Адрес прежней версии: отдаётся текущая, без долгого кеша.
		w.Header().Set("Cache-Control", "no-cache")
	}
	if data == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeTile(w, data, arch.Header().TileCompression)
}

// dataTile отдаёт тайл слоя данных. Доступ — по праву слоя; адрес содержит версию данных
// (параметр v из каталога), поэтому ответ кешируется браузером, но не общими кешами.
func (h *Handler) dataTile(w http.ResponseWriter, r *http.Request) {
	p, ok := h.d.Auth.Principal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	l, ok := layerByCode(r.PathValue("layer"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !p.Has(l.Permission) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	z, x, y, ok := tileCoords(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if int(z) < l.MinZoom || int(z) > l.MaxZoom {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data, version, err := h.d.Data.Tile(r.Context(), l, z, x, y)
	if err != nil {
		h.d.Log.ErrorContext(r.Context(), "data tile", "layer", l.Code, "z", z, "x", x, "y", y, "error", err)
		http.Error(w, "tile error", http.StatusInternalServerError)
		return
	}
	etag := `"` + l.Code + "-" + version + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "Accept-Encoding")
	if r.URL.Query().Get("v") == version {
		w.Header().Set("Cache-Control", "private, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if len(data) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		raw, err := gunzip(data)
		if err != nil {
			http.Error(w, "tile error", http.StatusInternalServerError)
			return
		}
		writeTile(w, raw, pmtiles.CompressionNone)
		return
	}
	writeTile(w, data, pmtiles.CompressionGzip)
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// glyphs отдаёт диапазон глифов; диапазоны вне набора — пустой ответ, а не ошибка, чтобы
// редкие символы в подписях не засоряли консоль браузера.
func (h *Handler) glyphs(w http.ResponseWriter, r *http.Request) {
	stack, rng := r.PathValue("stack"), r.PathValue("range")
	if !strings.HasSuffix(rng, ".pbf") || strings.ContainsAny(stack+rng, `/\`) {
		http.NotFound(w, r)
		return
	}
	// Стек «Noto Sans Medium,Noto Sans Regular» — берётся первый шрифт, который есть.
	var data []byte
	for name := range strings.SplitSeq(stack, ",") {
		b, err := fs.ReadFile(h.fonts, path.Join(strings.TrimSpace(name), rng))
		if err == nil {
			data = b
			break
		}
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(data)
}

func (h *Handler) sprite(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	b, err := fs.ReadFile(h.icons, name)
	if err != nil || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, "LICENSE") {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(name) {
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(b) //nolint:gosec // встроенный файл, тип содержимого задан явно
}

func (h *Handler) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNoBasemap) {
		http.Error(w, "basemap is not installed", http.StatusNotFound)
		return
	}
	h.d.Log.ErrorContext(r.Context(), "basemap", "error", err)
	http.Error(w, "basemap unavailable", http.StatusServiceUnavailable)
}

// writeTile отдаёт тайл как он хранится; сжатый gzip — с заголовком Content-Encoding.
func writeTile(w http.ResponseWriter, data []byte, compression uint8) {
	w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	if compression == pmtiles.CompressionGzip {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data) //nolint:gosec // двоичный тайл, тип содержимого задан явно
}

// tileCoords разбирает z/x/y из адреса вида …/{z}/{x}/{y}.mvt.
func tileCoords(r *http.Request) (uint8, uint32, uint32, bool) {
	ys, ok := strings.CutSuffix(r.PathValue("y"), ".mvt")
	if !ok {
		return 0, 0, 0, false
	}
	z, err1 := strconv.ParseUint(r.PathValue("z"), 10, 8)
	x, err2 := strconv.ParseUint(r.PathValue("x"), 10, 32)
	y, err3 := strconv.ParseUint(ys, 10, 32)
	if err1 != nil || err2 != nil || err3 != nil || z > 22 || x >= 1<<z || y >= 1<<z {
		return 0, 0, 0, false
	}
	return uint8(z), uint32(x), uint32(y), true
}

// origin — схема и хост запроса для абсолютных адресов в TileJSON (MapLibre не разрешает
// относительные адреса тайлов). За обратным прокси схему сообщает X-Forwarded-Proto.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

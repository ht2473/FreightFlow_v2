// Package basemap — загрузчик подложки: вырезка агломерации Москвы из ежедневной сборки
// Protomaps (векторные тайлы из OpenStreetMap и Natural Earth) в архив PMTiles.
package basemap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"freightflow/internal/ingest"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/outbound"
	"freightflow/internal/platform/pmtiles"
)

// Code — код источника.
const Code = "basemap"

const (
	buildsURL = "https://build-metadata.protomaps.dev/builds.json"
	buildBase = "https://build.protomaps.com/"
	// tilesMajor — версия схемы тайлов, под которую написаны стили клиента (@protomaps/basemaps 5).
	tilesMajor = "4."
	maxZoom    = 15
)

// PointerFile — указатель на текущий архив подложки относительно каталога данных. Архив не
// перезаписывается на месте (в Windows открытый файл нельзя заменить): новый архив
// остаётся снимком в хранилище, а указатель переписывается.
const PointerFile = "basemap/current.json"

// Ref — содержимое указателя.
type Ref struct {
	// Path — путь архива относительно каталога данных.
	Path string `json:"path"`
	// Version — версия для адресов тайлов (начало SHA-256 архива).
	Version string `json:"version"`
	// Build — сборка Protomaps, из которой вырезан архив.
	Build string `json:"build,omitempty"`
}

// BBox — рамка подложки: Москва и агломерация внутри ЦКАД с запасом (запад, юг, восток, север).
var BBox = [4]float64{36.30, 54.90, 39.00, 56.55}

// Config — параметры загрузчика.
type Config struct {
	// DataDir — каталог данных приложения: туда пишется указатель на текущий архив.
	DataDir string
}

// Source — загрузчик подложки.
type Source struct {
	cfg Config
}

// New создаёт загрузчик.
func New(cfg Config) *Source { return &Source{cfg: cfg} }

// Descriptor описывает источник.
func (s *Source) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        Code,
		Title:       "Подложка карты",
		Publisher:   "Protomaps, сборка из OpenStreetMap и Natural Earth",
		URL:         "https://maps.protomaps.com/builds/",
		License:     "ODbL 1.0 (данные OpenStreetMap)",
		Attribution: "Protomaps © участники OpenStreetMap",
		Domains:     []string{"basemap"},
		Interval:    30 * 24 * time.Hour,
		// Тайлы вырезает pmtiles напрямую из build.protomaps.com (запросы диапазонов); через
		// клиент приложения идёт только список сборок.
		Hosts:   []string{"build-metadata.protomaps.dev"},
		Queue:   jobs.QueueOSM,
		Tools:   []string{"pmtiles"},
		Timeout: 2 * time.Hour,
	}
}

// build — запись списка сборок.
type build struct {
	Key     string `json:"key"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
}

// Fetch вырезает подложку из последней сборки, если она новее загруженной.
func (s *Source) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	b, err := latestBuild(ctx, env)
	if err != nil {
		return ingest.Fetched{}, err
	}
	if env.Previous != nil && env.Previous.Version == b.Key {
		return ingest.Fetched{NotModified: true}, nil
	}
	if st, err := env.Stored(ctx, b.Key); err != nil {
		return ingest.Fetched{}, err
	} else if st != nil {
		env.Log.Info(ctx, "подложка этой сборки уже вырезана, используется сохранённый снимок")
		return ingest.Fetched{Object: blob.Object{Path: st.Path, SHA256: st.SHA256, Size: st.Size}, Format: st.Format, Version: b.Key}, nil
	}

	out := filepath.Join(env.TempDir, "basemap.pmtiles")
	env.Log.Info(ctx, fmt.Sprintf("вырезка из сборки %s (схема тайлов %s), масштабы 0–%d", b.Key, b.Version, maxZoom))
	if err := run(ctx, "pmtiles", "extract", buildBase+b.Key, out,
		fmt.Sprintf("--bbox=%g,%g,%g,%g", BBox[0], BBox[1], BBox[2], BBox[3]),
		fmt.Sprintf("--maxzoom=%d", maxZoom), "--download-threads=4"); err != nil {
		return ingest.Fetched{}, err
	}
	if err := run(ctx, "pmtiles", "verify", out); err != nil {
		return ingest.Fetched{}, fmt.Errorf("архив не прошёл проверку: %w", err)
	}
	f, err := os.Open(out)
	if err != nil {
		return ingest.Fetched{}, err
	}
	defer f.Close()
	obj, err := env.Store(ctx, "pmtiles", f)
	if err != nil {
		return ingest.Fetched{}, err
	}
	env.Log.Info(ctx, fmt.Sprintf("архив подложки: %d МБ", obj.Size>>20))
	return ingest.Fetched{Object: obj, Format: "pmtiles", Version: b.Key}, nil
}

// latestBuild — последняя сборка со схемой тайлов, которую понимают стили клиента.
func latestBuild(ctx context.Context, env ingest.FetchEnv) (build, error) {
	resp, err := env.HTTP.Get(ctx, buildsURL, outbound.Validators{})
	if err != nil {
		return build{}, err
	}
	defer resp.Body.Close()
	var builds []build
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&builds); err != nil {
		return build{}, fmt.Errorf("список сборок: %w", err)
	}
	builds = slices.DeleteFunc(builds, func(b build) bool {
		return !strings.HasPrefix(b.Version, tilesMajor) || !strings.HasSuffix(b.Key, ".pmtiles")
	})
	if len(builds) == 0 {
		return build{}, fmt.Errorf("нет сборок со схемой тайлов %sx", tilesMajor)
	}
	slices.SortFunc(builds, func(a, b build) int { return strings.Compare(a.Key, b.Key) })
	return builds[len(builds)-1], nil
}

// Load проверяет архив и делает его текущей подложкой.
func (s *Source) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	path, err := env.Blobs.LocalPath(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	arch, err := pmtiles.Open(path)
	if err != nil {
		return nil, err
	}
	h := arch.Header()
	x, y := tileOf(14, 37.6175, 55.7520)
	center, err := arch.Tile(14, x, y)
	_ = arch.Close()
	if err != nil {
		return nil, err
	}

	checks := []ingest.Check{
		{
			Code: "tile_type", Title: "Векторные тайлы MVT", Severity: ingest.SeverityCritical,
			Passed: h.TileType == pmtiles.TileTypeMVT, Observed: h.TileType, Expected: pmtiles.TileTypeMVT,
		},
		{
			Code: "zoom", Title: fmt.Sprintf("Масштабы 0–%d", maxZoom), Severity: ingest.SeverityCritical,
			Passed: h.MinZoom == 0 && h.MaxZoom == maxZoom, Observed: []uint8{h.MinZoom, h.MaxZoom},
		},
		{
			Code: "center", Title: "Есть тайл центра Москвы (z14)", Severity: ingest.SeverityCritical,
			Passed: len(center) > 0, Observed: len(center),
		},
		{
			Code: "tiles", Title: "Число тайлов в разумных пределах", Severity: ingest.SeverityWarning,
			Passed: h.AddressedTiles >= 20000 && h.AddressedTiles <= 500000, Observed: h.AddressedTiles,
			Expected: map[string]int{"min": 20000, "max": 500000},
		},
	}
	ok := true
	for _, c := range checks {
		env.Check(ctx, c)
		ok = ok && (c.Passed || c.Severity != ingest.SeverityCritical)
	}
	if !ok {
		return nil, ingest.ErrQualityFailed
	}

	ref := Ref{Path: env.Snapshot.Path, Version: env.Snapshot.SHA256[:12], Build: env.Snapshot.Version}
	if err := writePointer(filepath.Join(s.cfg.DataDir, filepath.FromSlash(PointerFile)), ref); err != nil {
		return nil, err
	}
	//nolint:gosec // число тайлов архива помещается в int64
	return ingest.LoadStats{"records": int64(h.AddressedTiles), "size_mb": env.Snapshot.Size >> 20}, nil
}

// writePointer заменяет указатель через временный файл. Сервер читает указатель коротко,
// поэтому замена при случайном совпадении повторяется.
func writePointer(path string, ref Ref) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		err = os.Rename(tmp, path)
		if err == nil || attempt == 4 {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// tileOf — тайл с точкой на масштабе z.
func tileOf(z uint8, lon, lat float64) (uint32, uint32) {
	n := math.Exp2(float64(z))
	r := lat * math.Pi / 180
	return uint32((lon + 180) / 360 * n), uint32((1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n)
}

// run запускает внешнюю программу; при ошибке возвращает конец её вывода.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return fmt.Errorf("%s: %w: %s", name, err, tail)
	}
	return nil
}

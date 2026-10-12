// Package osm — загрузчик OpenStreetMap: выгрузка Geofabrik по Центральному федеральному
// округу, вырезка агломерации Москвы (osmium), импорт в osm_stage (osm2pgsql, flex) и
// перенос в домен: территории, кольца, объекты логистики, инфраструктура, дорожная сеть.
package osm

import (
	"bufio"
	"context"
	"crypto/md5" //nolint:gosec // контрольная сумма Geofabrik публикуется в MD5
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/outbound"
)

// Code — код источника.
const Code = "osm"

const geofabrik = "https://download.geofabrik.de/"

// RoutingFile — выгрузка Москвы и области для графа маршрутизации (относительно OutputDir).
const RoutingFile = "routing.osm.pbf"

// Config — параметры загрузчика.
type Config struct {
	// DatabaseURL — подключение для osm2pgsql.
	DatabaseURL string
	// Region — выгрузка Geofabrik; по умолчанию russia/central-fed-district.
	Region string
	// Processes — параллелизм osm2pgsql; 0 — 4.
	Processes int
	// OutputDir — каталог выгрузок для других служб (граф маршрутизации).
	OutputDir string
}

// Source — загрузчик OpenStreetMap.
type Source struct {
	cfg Config
}

// New создаёт загрузчик.
func New(cfg Config) *Source {
	if cfg.Region == "" {
		cfg.Region = "russia/central-fed-district"
	}
	if cfg.Processes == 0 {
		cfg.Processes = 4
	}
	return &Source{cfg: cfg}
}

// Descriptor описывает источник.
func (s *Source) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        Code,
		Title:       "OpenStreetMap",
		Publisher:   "Участники OpenStreetMap, выгрузка Geofabrik",
		URL:         geofabrik + s.cfg.Region + ".html",
		License:     "ODbL 1.0",
		Attribution: "© участники OpenStreetMap",
		Domains:     []string{"territory", "facility", "infra", "network"},
		Interval:    7 * 24 * time.Hour,
		Hosts:       []string{"download.geofabrik.de"},
		Queue:       jobs.QueueOSM,
		Tools:       []string{"osmium", "osm2pgsql"},
		Timeout:     4 * time.Hour,
		// Выгрузка округа — около 1,5 ГБ.
		RequestTimeout: 2 * time.Hour,
	}
}

// Fetch загружает выгрузку, если её контрольная сумма изменилась. Файл качается в рабочий
// каталог источника и при обрыве докачивается — в том числе следующим запуском.
func (s *Source) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	base := geofabrik + s.cfg.Region + "-latest.osm.pbf"
	sum, err := s.remoteMD5(ctx, env.HTTP, base+".md5")
	if err != nil {
		return ingest.Fetched{}, err
	}
	if env.Previous != nil && env.Previous.Version == sum {
		return ingest.Fetched{NotModified: true}, nil
	}
	if st, err := env.Stored(ctx, sum); err != nil {
		return ingest.Fetched{}, err
	} else if st != nil {
		env.Log.Info(ctx, "выгрузка этой версии уже получена, используется сохранённый снимок")
		return ingest.Fetched{
			Object: blob.Object{Path: st.Path, SHA256: st.SHA256, Size: st.Size},
			Format: st.Format, Version: sum, Validators: st.Validators,
		}, nil
	}

	part := filepath.Join(env.WorkDir, sum+".osm.pbf.part")
	if err := removeStale(env.WorkDir, part); err != nil {
		return ingest.Fetched{}, err
	}
	env.Log.Info(ctx, "загрузка выгрузки "+s.cfg.Region)
	var validators outbound.Validators
	for attempt := 1; ; attempt++ {
		validators, err = download(ctx, env, base, part)
		if err == nil {
			break
		}
		if ctx.Err() != nil || attempt == maxAttempts {
			return ingest.Fetched{}, err
		}
		env.Log.Warn(ctx, fmt.Sprintf("обрыв загрузки, продолжение (%d из %d): %v", attempt, maxAttempts-1, err))
	}

	if got, err := fileMD5(part); err != nil {
		return ingest.Fetched{}, err
	} else if got != sum {
		_ = os.Remove(part)
		return ingest.Fetched{}, fmt.Errorf("контрольная сумма не совпала: %s, ожидалась %s", got, sum)
	}
	f, err := os.Open(part)
	if err != nil {
		return ingest.Fetched{}, err
	}
	obj, err := env.Store(ctx, "osm.pbf", f)
	_ = f.Close()
	if err != nil {
		return ingest.Fetched{}, err
	}
	_ = os.Remove(part)
	return ingest.Fetched{Object: obj, Format: "osm.pbf", Version: sum, Validators: validators}, nil
}

// maxAttempts — попыток докачки за один запуск.
const maxAttempts = 6

// download дописывает файл с того места, где загрузка прервалась.
func download(ctx context.Context, env ingest.FetchEnv, url, part string) (outbound.Validators, error) {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return outbound.Validators{}, err
	}
	defer f.Close()
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return outbound.Validators{}, err
	}
	resp, err := env.HTTP.GetFrom(ctx, url, offset)
	if err != nil {
		return outbound.Validators{}, err
	}
	defer resp.Body.Close()
	total := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		total += offset
		if offset > 0 {
			env.Log.Info(ctx, fmt.Sprintf("продолжение с %d МБ", offset>>20))
		}
	} else {
		// Сервер отдал файл целиком: пишем с начала.
		if err := f.Truncate(0); err != nil {
			return outbound.Validators{}, err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return outbound.Validators{}, err
		}
		offset = 0
	}
	progress := &progressReader{r: resp.Body, total: total, read: offset, next: offset + step, log: env.Log, ctx: ctx}
	if _, err := io.Copy(f, progress); err != nil {
		return outbound.Validators{}, fmt.Errorf("read body: %w", err)
	}
	return resp.Validators, f.Sync()
}

// removeStale удаляет недокачанные файлы прежних выгрузок.
func removeStale(dir, keep string) error {
	parts, err := filepath.Glob(filepath.Join(dir, "*.osm.pbf.part"))
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p != keep {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New() //nolint:gosec // сверка с опубликованной суммой
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Source) remoteMD5(ctx context.Context, c *outbound.Client, url string) (string, error) {
	resp, err := c.Get(ctx, url, outbound.Validators{})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(io.LimitReader(resp.Body, 1024)).ReadString(' ')
	if err != nil && line == "" {
		return "", fmt.Errorf("read md5: %w", err)
	}
	sum := strings.TrimSpace(line)
	if len(sum) != 32 {
		return "", fmt.Errorf("unexpected md5 file: %q", line)
	}
	return sum, nil
}

// Load вырезает регионы, импортирует город и переносит его в домен; выгрузка для
// маршрутизации сохраняется в OutputDir.
func (s *Source) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	src, err := env.Blobs.LocalPath(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	env.Log.Info(ctx, "вырезка регионов (osmium)")
	city, routing, err := extractRegions(ctx, env.TempDir, src)
	if err != nil {
		return nil, err
	}
	if s.cfg.OutputDir != "" {
		if err := install(routing, filepath.Join(s.cfg.OutputDir, RoutingFile)); err != nil {
			return nil, fmt.Errorf("save routing extract: %w", err)
		}
	}

	style := filepath.Join(env.TempDir, "flex.lua")
	if err := copyEmbedded("osm/flex.lua", style); err != nil {
		return nil, err
	}
	env.Log.Info(ctx, "импорт в osm_stage (osm2pgsql)")
	if err := run(ctx, "osm2pgsql", "--create", "--slim", "--drop",
		"--output=flex", "--style="+style, "--schema=osm_stage",
		fmt.Sprintf("--number-processes=%d", s.cfg.Processes),
		"--log-level=warn", "-d", s.cfg.DatabaseURL, city); err != nil {
		return nil, err
	}

	transform, err := etl.FS.ReadFile("osm/transform.sql")
	if err != nil {
		return nil, err
	}
	env.Log.Info(ctx, "перенос в домен")
	var stats ingest.LoadStats
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true)`,
			Code, env.Snapshot.ID.String()); err != nil {
			return fmt.Errorf("set transform parameters: %w", err)
		}
		// Скрипт из нескольких операторов выполняется простым протоколом (без параметров).
		if _, err := tx.Exec(ctx, string(transform)); err != nil {
			return fmt.Errorf("transform: %w", err)
		}
		stats, err = collectStats(ctx, tx)
		if err != nil {
			return err
		}
		if !checkQuality(ctx, env, stats) {
			return ingest.ErrQualityFailed
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	return stats, nil
}

// extractRegions за один проход вырезает город (с агломерацией внутри ЦКАД) и регион
// маршрутизации по многоугольникам etl/regions.
func extractRegions(ctx context.Context, dir, src string) (string, string, error) {
	type extract struct {
		Output  string         `json:"output"`
		Polygon map[string]any `json:"polygon"`
	}
	cfg := struct {
		Directory string    `json:"directory"`
		Extracts  []extract `json:"extracts"`
	}{Directory: dir}
	for _, name := range []string{"city", "routing"} {
		poly := filepath.Join(dir, name+".geojson")
		if err := copyEmbedded("regions/"+name+".geojson", poly); err != nil {
			return "", "", err
		}
		cfg.Extracts = append(cfg.Extracts, extract{
			Output:  name + ".osm.pbf",
			Polygon: map[string]any{"file_name": poly, "file_type": "geojson"},
		})
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", "", err
	}
	conf := filepath.Join(dir, "extracts.json")
	if err := os.WriteFile(conf, b, 0o600); err != nil {
		return "", "", err
	}
	if err := run(ctx, "osmium", "extract", "-c", conf,
		"-s", "smart", "-S", "types=multipolygon,boundary,route",
		"--overwrite", src); err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "city.osm.pbf"), filepath.Join(dir, "routing.osm.pbf"), nil
}

// install переносит файл в каталог выгрузок с заменой через временное имя.
func install(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// collectStats считает результат переноса.
func collectStats(ctx context.Context, tx pgx.Tx) (ingest.LoadStats, error) {
	queries := map[string]string{
		"records":          `SELECT count(*) FROM infra.facility WHERE source_code = 'osm' AND status = 'active'`,
		"facilities_new":   `SELECT count(*) FROM infra.facility WHERE source_code = 'osm' AND first_seen_at = now()`,
		"facilities_gone":  `SELECT count(*) FROM infra.facility WHERE source_code = 'osm' AND status = 'missing' AND updated_at = now()`,
		"regions":          `SELECT count(*) FROM territory.admin_area WHERE kind = 'region'`,
		"okrugs":           `SELECT count(*) FROM territory.admin_area WHERE kind = 'okrug'`,
		"districts":        `SELECT count(*) FROM territory.admin_area WHERE kind = 'district'`,
		"municipal":        `SELECT count(*) FROM territory.admin_area WHERE kind = 'municipal_district'`,
		"rings":            `SELECT count(*) FROM territory.ring_zone WHERE zone IS NOT NULL`,
		"industrial_zones": `SELECT count(*) FROM infra.industrial_zone`,
		"poi":              `SELECT count(*) FROM infra.poi`,
		"roads":            `SELECT count(*) FROM network.road_segment`,
		"restrictions":     `SELECT count(*) FROM network.restriction WHERE origin = 'osm'`,
		"bridges":          `SELECT count(*) FROM network.bridge`,
	}
	stats := ingest.LoadStats{}
	for k, q := range queries {
		var n int64
		if err := tx.QueryRow(ctx, q).Scan(&n); err != nil {
			return nil, fmt.Errorf("stat %s: %w", k, err)
		}
		stats[k] = n
	}
	return stats, nil
}

// checkQuality проверяет полноту переноса. Возвращает false при критическом нарушении.
func checkQuality(ctx context.Context, env ingest.LoadEnv, st ingest.LoadStats) bool {
	type rule struct {
		code, title string
		severity    ingest.Severity
		value       int64
		lo, hi      int64
	}
	rules := []rule{
		{"regions", "Москва и Московская область на месте", ingest.SeverityCritical, st["regions"], 2, 2},
		{"okrugs", "12 административных округов Москвы", ingest.SeverityCritical, st["okrugs"], 12, 12},
		{"districts", "Районы Москвы (132 по границам OSM 2026 г.)", ingest.SeverityWarning, st["districts"], 125, 140},
		{"rings", "Зоны всех четырёх колец построены", ingest.SeverityWarning, st["rings"], 4, 4},
		{"facilities", "Число объектов логистики в разумных пределах", ingest.SeverityCritical, st["records"], 300, 30000},
		{"roads", "Дорожная сеть загружена", ingest.SeverityCritical, st["roads"], 50000, 2000000},
		{"restrictions", "Ограничения на сети найдены", ingest.SeverityWarning, st["restrictions"], 100, 1000000},
	}
	ok := true
	for _, r := range rules {
		passed := r.value >= r.lo && r.value <= r.hi
		if !passed && r.severity == ingest.SeverityCritical {
			ok = false
		}
		env.Check(ctx, ingest.Check{
			Code: r.code, Title: r.title, Severity: r.severity, Passed: passed,
			Observed: r.value, Expected: map[string]int64{"min": r.lo, "max": r.hi},
		})
	}
	return ok
}

// run запускает внешнюю программу; при ошибке возвращает конец её вывода.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var out tailBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(out.String()))
	}
	return nil
}

func copyEmbedded(name, dst string) error {
	b, err := etl.FS.ReadFile(name)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

// tailBuffer хранит последние 4 КБ вывода программы.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

// step — шаг сообщений о ходе загрузки.
const step = 100 << 20

// progressReader сообщает в журнал запуска о ходе загрузки каждые 100 МБ.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	next  int64
	log   ingest.RunLog
	ctx   context.Context //nolint:containedctx // журнал пишется из Read
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.read >= p.next {
		msg := fmt.Sprintf("получено %d МБ", p.read>>20)
		if p.total > 0 {
			msg += fmt.Sprintf(" из %d МБ", p.total>>20)
		}
		p.log.Info(p.ctx, msg)
		p.next += step
	}
	return n, err
}

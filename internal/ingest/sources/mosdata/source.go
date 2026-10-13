package mosdata

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
)

// Dataset — набор портала и правила его переноса в домен.
type Dataset struct {
	// ID — номер набора на портале.
	ID int
	// Code — код источника в реестре (mos_<суть>).
	Code  string
	Title string
	// Domains — домены, версии которых растут после загрузки.
	Domains  []string
	Interval time.Duration
	// Geo — набор с геоданными: выгрузка через /features (геометрия и атрибуты).
	Geo bool
	// Fields — только эти атрибуты (проекция в запросе): так персональные данные и лишние
	// поля не попадают даже в сырой снимок. Пусто — все атрибуты.
	Fields []string
	// Transform — SQL-преобразование etl/mosdata/<файл>; читает mos_stage.record набора.
	Transform string
	// MinRecords — меньше записей — критическая ошибка проверки (набор пуст или обрезан).
	MinRecords int64
	// Event — доменное событие после загрузки (итог загрузки в данных события).
	Event string
	// Checks — проверки итогов переноса (значения load_stats).
	Checks []StatCheck
}

// StatCheck — проверка значения итога переноса на попадание в диапазон.
type StatCheck struct {
	Code     string
	Title    string
	Severity ingest.Severity
	Stat     string
	Min, Max int64
}

// evaluate записывает результаты проверок; false — нарушена критическая.
func evaluate(ctx context.Context, env ingest.LoadEnv, checks []StatCheck, stats ingest.LoadStats) bool {
	ok := true
	for _, c := range checks {
		v := stats[c.Stat]
		passed := v >= c.Min && v <= c.Max
		if !passed && c.Severity == ingest.SeverityCritical {
			ok = false
		}
		env.Check(ctx, ingest.Check{
			Code: c.Code, Title: c.Title, Severity: c.Severity, Passed: passed, Observed: v,
			Expected: map[string]int64{"min": c.Min, "max": c.Max},
		})
	}
	return ok
}

// Record — строка снимка: идентификатор записи, атрибуты, геометрия GeoJSON (если есть).
type Record struct {
	ID       int64           `json:"id"`
	Cells    json.RawMessage `json:"cells"`
	Geometry json.RawMessage `json:"geometry,omitempty"`
}

// Source — загрузчик одного набора портала.
type Source struct {
	ds Dataset
}

// New создаёт загрузчик набора.
func New(ds Dataset) *Source { return &Source{ds: ds} }

// SecretKey — переменная окружения с ключом API портала.
const SecretKey = "FF_MOSDATA_API_KEY" //nolint:gosec // имя переменной окружения, не ключ

// Attribution — атрибуция портала (CC BY 4.0: ссылка на первоисточник).
const Attribution = "Портал открытых данных Правительства Москвы, data.mos.ru"

// Descriptor описывает источник.
func (s *Source) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        s.ds.Code,
		Title:       s.ds.Title,
		Publisher:   "Портал открытых данных Правительства Москвы, набор " + strconv.Itoa(s.ds.ID),
		URL:         "https://data.mos.ru/opendata/" + strconv.Itoa(s.ds.ID),
		License:     "CC BY 4.0",
		Attribution: Attribution + ", набор " + strconv.Itoa(s.ds.ID),
		Domains:     s.ds.Domains,
		Interval:    s.ds.Interval,
		Hosts:       []string{Host},
		Secrets:     []string{SecretKey},
		Timeout:     3 * time.Hour,
		// Страница до 1000 записей с геометрией — несколько мегабайт.
		RequestTimeout: 5 * time.Minute,
	}
}

// Fetch выгружает набор, если его версия изменилась.
func (s *Source) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	c := NewClient(env.HTTP, env.Secret(SecretKey))
	ver, err := c.Version(ctx, s.ds.ID)
	if err != nil {
		return ingest.Fetched{}, fmt.Errorf("версия набора: %w", err)
	}
	version := ver.String()
	if env.Previous != nil && env.Previous.Version == version {
		return ingest.Fetched{NotModified: true}, nil
	}
	// Снимок этой версии, в котором меньше записей, чем нужно для загрузки (выгрузка
	// оборвалась), не используется — набор выгружается заново.
	if st, err := env.Stored(ctx, version); err != nil {
		return ingest.Fetched{}, err
	} else if st != nil && st.RecordCount != nil && *st.RecordCount >= s.ds.MinRecords {
		env.Log.Info(ctx, "эта версия набора уже получена, используется сохранённый снимок")
		return storedFetched(st, version), nil
	}

	p, err := c.Passport(ctx, s.ds.ID)
	if err != nil {
		return ingest.Fetched{}, fmt.Errorf("паспорт набора: %w", err)
	}
	schema, err := json.Marshal(p.Columns)
	if err != nil {
		return ingest.Fetched{}, err
	}
	total, err := c.Count(ctx, s.ds.ID)
	if err != nil {
		return ingest.Fetched{}, fmt.Errorf("число записей: %w", err)
	}
	env.Log.Info(ctx, fmt.Sprintf("набор %d, версия %s, записей %d", s.ds.ID, version, total))

	dir := env.WorkDir
	if dir == "" {
		dir = env.TempDir
	}
	path, n, err := s.download(ctx, c, dir, version, total, env.Log)
	if err != nil {
		return ingest.Fetched{}, err
	}
	obj, err := storeGzip(ctx, env, path)
	if err != nil {
		return ingest.Fetched{}, err
	}
	// Выгрузка сохранена в снимок — недокачанные файлы больше не нужны.
	removePartial(dir, s.ds.ID)
	return ingest.Fetched{Object: obj, Format: "jsonl.gz", Version: version, RecordCount: &n, Schema: schema}, nil
}

// progress — докуда дошла выгрузка версии набора: следующая страница, подтверждённая длина
// файла записей и число записей в нём.
type progress struct {
	Next    int   `json:"next"`
	Offset  int64 `json:"offset"`
	Records int64 `json:"records"`
}

func partialName(dir string, id int, version string) string {
	return filepath.Join(dir, fmt.Sprintf("%d-%s", id, version))
}

// removePartial удаляет недокачанные выгрузки набора (всех версий).
func removePartial(dir string, id int) {
	files, _ := filepath.Glob(filepath.Join(dir, strconv.Itoa(id)+"-*"))
	for _, f := range files {
		_ = os.Remove(f)
	}
}

// download пишет записи набора в файл JSON Lines в каталоге dir. После каждой страницы
// отмечается, докуда дошла выгрузка, поэтому после обрыва (перезапуск, сбой портала) она
// продолжается с той же страницы этой версии набора.
func (s *Source) download(ctx context.Context, c *Client, dir, version string, total int64, log ingest.RunLog) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, err
	}
	base := partialName(dir, s.ds.ID, version)
	path, mark := base+".jsonl", base+".progress"
	var pr progress
	if b, err := os.ReadFile(mark); err == nil && json.Unmarshal(b, &pr) == nil && pr.Next > 0 {
		log.Info(ctx, fmt.Sprintf("выгрузка продолжается с записи %d (получено %d)", pr.Next, pr.Records))
	} else {
		removePartial(dir, s.ds.ID)
		pr = progress{}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	// Записи после последней отметки (страница, прерванная на середине) отбрасываются.
	if err := f.Truncate(pr.Offset); err != nil {
		return "", 0, err
	}
	if _, err := f.Seek(pr.Offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	enc := json.NewEncoder(bw)
	report := (pr.Records/(50*PageSize) + 1) * 50 * PageSize
	page := func(recs []Record, next int) error {
		for _, r := range recs {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		off, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		pr.Records += int64(len(recs))
		pr.Next, pr.Offset = next, off
		b, err := json.Marshal(pr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(mark, b, 0o600); err != nil {
			return err
		}
		if pr.Records >= report {
			log.Info(ctx, fmt.Sprintf("получено %d из %d записей", pr.Records, total))
			report += 50 * PageSize
		}
		return nil
	}
	if s.ds.Geo {
		err = c.EachFeaturePage(ctx, s.ds.ID, total, pr.Next, page)
	} else {
		err = c.EachRowPage(ctx, s.ds.ID, s.ds.Fields, total, pr.Next, page)
	}
	if err != nil {
		return "", 0, err
	}
	return path, pr.Records, f.Close()
}

// storeGzip сохраняет файл записей в снимок со сжатием.
func storeGzip(ctx context.Context, env ingest.FetchEnv, path string) (blob.Object, error) {
	src, err := os.Open(path)
	if err != nil {
		return blob.Object{}, err
	}
	defer src.Close()
	pr, pw := io.Pipe()
	go func() {
		zw := gzip.NewWriter(pw)
		_, err := io.Copy(zw, src)
		if err == nil {
			err = zw.Close()
		}
		pw.CloseWithError(err)
	}()
	obj, err := env.Store(ctx, "jsonl.gz", pr)
	_ = pr.CloseWithError(err)
	return obj, err
}

// Load копирует снимок в mos_stage.record и переносит его в домен преобразованием набора.
func (s *Source) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	script, err := etl.FS.ReadFile("mosdata/" + s.ds.Transform)
	if err != nil {
		return nil, err
	}
	f, err := env.Blobs.Open(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var stats ingest.LoadStats
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		staged, err := stage(ctx, tx, s.ds.ID, zr)
		if err != nil {
			return err
		}
		env.Check(ctx, ingest.Check{
			Code: "records", Title: "Записей в наборе не меньше ожидаемого", Severity: ingest.SeverityCritical,
			Passed: staged >= s.ds.MinRecords, Observed: staged, Expected: map[string]int64{"min": s.ds.MinRecords},
		})
		if staged < s.ds.MinRecords {
			return ingest.ErrQualityFailed
		}
		env.Log.Info(ctx, fmt.Sprintf("перенос в домен: %d записей", staged))
		stats, err = transform(ctx, tx, s.ds, env.Snapshot.ID, string(script))
		if err != nil {
			return err
		}
		stats["records"] = staged
		if !evaluate(ctx, env, s.ds.Checks, stats) {
			return ingest.ErrQualityFailed
		}
		if s.ds.Event != "" {
			return events.Publish(ctx, tx, s.ds.Event, map[string]any{"source": s.ds.Code, "dataset": s.ds.ID, "stats": stats})
		}
		return nil
	})
	return stats, err
}

// stage заменяет записи набора в промежуточной таблице.
func stage(ctx context.Context, tx pgx.Tx, dataset int, r io.Reader) (int64, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM mos_stage.record WHERE dataset = $1`, dataset); err != nil {
		return 0, fmt.Errorf("clear stage: %w", err)
	}
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	// Пока идёт постраничная выгрузка, набор может меняться: страницы сдвигаются, и запись
	// попадает в выгрузку дважды. Остаётся первая.
	seen := map[int64]struct{}{}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"mos_stage", "record"}, []string{"dataset", "global_id", "cells", "geometry"},
		pgx.CopyFromFunc(func() ([]any, error) {
			var rec Record
			for {
				rec = Record{}
				if err := dec.Decode(&rec); err != nil {
					if errors.Is(err, io.EOF) {
						return nil, nil
					}
					return nil, err
				}
				if _, dup := seen[rec.ID]; !dup {
					break
				}
			}
			seen[rec.ID] = struct{}{}
			var geom any
			if len(rec.Geometry) > 0 && string(rec.Geometry) != "null" {
				geom = []byte(rec.Geometry)
			}
			return []any{dataset, rec.ID, []byte(rec.Cells), geom}, nil
		}))
	if err != nil {
		return 0, fmt.Errorf("stage records: %w", err)
	}
	return n, nil
}

// transform выполняет преобразование набора и считает итог: записи домена с этим источником.
func transform(ctx context.Context, tx pgx.Tx, ds Dataset, snapshot uuid.UUID, script string) (ingest.LoadStats, error) {
	if _, err := tx.Exec(ctx, `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true),
		set_config('ff.dataset', $3, true)`, ds.Code, snapshot.String(), strconv.Itoa(ds.ID)); err != nil {
		return nil, fmt.Errorf("set transform parameters: %w", err)
	}
	// Скрипт из нескольких операторов выполняется простым протоколом (без параметров).
	if _, err := tx.Exec(ctx, script); err != nil {
		return nil, fmt.Errorf("transform %s: %w", ds.Transform, err)
	}
	// Итог преобразования скрипт оставляет во временной таблице load_stats (код → число).
	stats := ingest.LoadStats{}
	rows, err := tx.Query(ctx, `SELECT key, value FROM load_stats`)
	if err != nil {
		return nil, fmt.Errorf("transform %s: load_stats: %w", ds.Transform, err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		stats[k] = v
	}
	return stats, rows.Err()
}

func storedFetched(st *ingest.Snapshot, version string) ingest.Fetched {
	f := ingest.Fetched{Format: st.Format, Version: version, RecordCount: st.RecordCount}
	f.Object.Path, f.Object.SHA256, f.Object.Size = st.Path, st.SHA256, st.Size
	return f
}

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/outbound"
)

// CargoCode — код источника сведений о грузовом транспорте с transport.mos.ru.
const CargoCode = "transport_cargo"

const site = "https://transport.mos.ru"

// pages — страницы, по которым сверены правила зон (коды документов ref.legal_document).
var pages = []struct{ Code, Path string }{
	{"tm-cargo-pass", "/gruzoviki/perevod_ckad"},
	{"tm-cargo-faq", "/gruzoviki/faq"},
	{"tm-cargo-frame", "/gruzoviki/cargoframe"},
	{"tm-cargo-frame-streets", "/gruzoviki/ulitsy"},
}

// streetsPage — страница с перечнем улиц грузового каркаса.
const streetsPage = "tm-cargo-frame-streets"

// minStreets — в перечне около 560 строк; заметно меньше — разметка страницы изменилась.
const minStreets = 400

// Snapshot — содержимое снимка: перечень улиц и тексты страниц.
type Snapshot struct {
	Streets []Street `json:"streets"`
	Pages   []Page   `json:"pages"`
}

// Cargo — загрузчик перечня улиц грузового каркаса и страниц правил.
type Cargo struct{}

// NewCargo создаёт загрузчик.
func NewCargo() *Cargo { return &Cargo{} }

// Descriptor описывает источник.
func (c *Cargo) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        CargoCode,
		Title:       "Грузовой каркас и правила для грузового транспорта",
		Publisher:   "Департамент транспорта Москвы, Единый транспортный портал",
		URL:         site + "/gruzoviki/ulitsy",
		License:     "Официальная информация органа власти (149-ФЗ, 8-ФЗ), с указанием источника",
		Attribution: "Дептранс Москвы, transport.mos.ru",
		Domains:     []string{"regulation"},
		Interval:    7 * 24 * time.Hour,
		Hosts:       []string{"transport.mos.ru"},
		RuNetwork:   true,
		Timeout:     10 * time.Minute,
	}
}

// Fetch загружает страницы раздела «Грузовая логистика» и сохраняет перечень улиц и тексты.
func (c *Cargo) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	var snap Snapshot
	for _, p := range pages {
		raw, err := get(ctx, env.HTTP, site+p.Path)
		if err != nil {
			return ingest.Fetched{}, fmt.Errorf("страница %s: %w", p.Path, err)
		}
		page, err := ParsePage(p.Code, site+p.Path, raw)
		if err != nil {
			return ingest.Fetched{}, err
		}
		snap.Pages = append(snap.Pages, page)
		if p.Code == streetsPage {
			if snap.Streets, err = ParseStreets(raw); err != nil {
				return ingest.Fetched{}, err
			}
		}
	}
	env.Log.Info(ctx, fmt.Sprintf("улиц в перечне: %d, страниц: %d", len(snap.Streets), len(snap.Pages)))
	b, err := json.Marshal(snap)
	if err != nil {
		return ingest.Fetched{}, err
	}
	obj, err := env.Store(ctx, "json", bytes.NewReader(b))
	if err != nil {
		return ingest.Fetched{}, err
	}
	n := int64(len(snap.Streets))
	return ingest.Fetched{Object: obj, Format: "json", RecordCount: &n}, nil
}

func get(ctx context.Context, c *outbound.Client, u string) (string, error) {
	resp, err := c.Get(ctx, u, outbound.Validators{})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

// Load сопоставляет улицы перечня с участками сети и обновляет тексты страниц
// (etl/transport/cargo.sql).
func (c *Cargo) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	f, err := env.Blobs.Open(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var snap Snapshot
	if err := json.NewDecoder(f).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	streets := int64(len(snap.Streets))
	env.Check(ctx, ingest.Check{
		Code: "streets", Title: fmt.Sprintf("Улиц в перечне не меньше %d", minStreets), Severity: ingest.SeverityCritical,
		Passed: streets >= minStreets, Observed: streets, Expected: map[string]int64{"min": minStreets},
	})
	if streets < minStreets {
		return nil, ingest.ErrQualityFailed
	}
	script, err := etl.FS.ReadFile("transport/cargo.sql")
	if err != nil {
		return nil, err
	}
	var stats ingest.LoadStats
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE tm_street (ord int, okrug text, entry text) ON COMMIT DROP;
			CREATE TEMP TABLE tm_page (code text, url text, title text, content text, content_hash text) ON COMMIT DROP`); err != nil {
			return err
		}
		rows := make([][]any, len(snap.Streets))
		for i, s := range snap.Streets {
			rows[i] = []any{i + 1, s.Okrug, s.Entry}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tm_street"}, []string{"ord", "okrug", "entry"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("stage streets: %w", err)
		}
		rows = make([][]any, len(snap.Pages))
		for i, p := range snap.Pages {
			rows[i] = []any{p.Code, p.URL, p.Title, p.Content, p.Hash}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tm_page"}, []string{"code", "url", "title", "content", "content_hash"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("stage pages: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true)`,
			CargoCode, env.Snapshot.ID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(script)); err != nil {
			return fmt.Errorf("transform cargo frame: %w", err)
		}
		if stats, err = loadStats(ctx, tx); err != nil {
			return err
		}
		pct := stats["streets_matched_pct"]
		env.Check(ctx, ingest.Check{
			Code: "streets_matched_pct", Title: "Улицы перечня найдены в дорожной сети (не меньше 75 %)",
			Severity: ingest.SeverityWarning, Passed: pct >= 75, Observed: pct, Expected: map[string]int64{"min": 75},
		})
		changed := stats["pages_changed"]
		env.Check(ctx, ingest.Check{
			Code: "pages_changed", Title: "Страницы правил не менялись после сверки правил зон",
			Severity: ingest.SeverityWarning, Passed: changed == 0, Observed: changed, Expected: map[string]int64{"max": 0},
		})
		return nil
	})
	return stats, err
}

func loadStats(ctx context.Context, tx pgx.Tx) (ingest.LoadStats, error) {
	rows, err := tx.Query(ctx, ingest.LoadStatsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := ingest.LoadStats{}
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

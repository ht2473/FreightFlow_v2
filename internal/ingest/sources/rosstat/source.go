package rosstat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/outbound"
)

// PopulationCode — код источника численности населения муниципальных образований.
const PopulationCode = "rosstat_population"

const (
	pmoURL = "https://rosstat.gov.ru/dbscripts/munst/munst45/DBInet.cgi"
	// populationIndicator — «Оценка численности населения на 1 января текущего года».
	populationIndicator = "8112027"
	formType            = "application/x-www-form-urlencoded"
)

// Population — загрузчик численности населения муниципальных образований Москвы из БД ПМО.
type Population struct{}

// NewPopulation создаёт загрузчик.
func NewPopulation() *Population { return &Population{} }

// Descriptor описывает источник.
func (p *Population) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        PopulationCode,
		Title:       "Численность населения муниципальных образований Москвы",
		Publisher:   "Росстат, база данных показателей муниципальных образований",
		URL:         pmoURL + "?pl=" + populationIndicator,
		License:     "Официальная статистика, общедоступная информация (149-ФЗ), с указанием источника",
		Attribution: "Росстат, база данных показателей муниципальных образований",
		Domains:     []string{"statistics", "territory"},
		Interval:    30 * 24 * time.Hour,
		Hosts:       []string{"rosstat.gov.ru"},
		RuNetwork:   true,
		Timeout:     30 * time.Minute,
		// Таблица по всем муниципальным образованиям и годам строится сервером долго.
		RequestTimeout: 5 * time.Minute,
	}
}

// Fetch запрашивает форму показателя (коды измерений), затем таблицу по всем муниципальным
// образованиям и годам и сохраняет записи в JSON.
func (p *Population) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	page, err := post(ctx, env.HTTP, url.Values{"pl": {populationIndicator}}.Encode())
	if err != nil {
		return ingest.Fetched{}, fmt.Errorf("форма показателя: %w", err)
	}
	f, err := parseForm(page)
	if err != nil {
		return ingest.Fetched{}, err
	}
	env.Log.Info(ctx, fmt.Sprintf("муниципальных образований: %d, годы %s–%s", len(f.oktmo), f.years[0], f.years[len(f.years)-1]))
	table, err := post(ctx, env.HTTP, string(f.query()))
	if err != nil {
		return ingest.Fetched{}, fmt.Errorf("таблица показателя: %w", err)
	}
	obs, err := parseTable(table, f)
	if err != nil {
		return ingest.Fetched{}, err
	}
	b, err := json.Marshal(obs)
	if err != nil {
		return ingest.Fetched{}, err
	}
	obj, err := env.Store(ctx, "json", bytes.NewReader(b))
	if err != nil {
		return ingest.Fetched{}, err
	}
	n := int64(len(obs))
	return ingest.Fetched{Object: obj, Format: "json", Version: f.years[len(f.years)-1], RecordCount: &n}, nil
}

func post(ctx context.Context, c *outbound.Client, body string) (string, error) {
	resp, err := c.Post(ctx, pmoURL, formType, []byte(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	return decode(raw)
}

// Load переносит записи снимка в домен (etl/rosstat/population.sql).
func (p *Population) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	f, err := env.Blobs.Open(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var obs []observation
	if err := json.NewDecoder(f).Decode(&obs); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	script, err := etl.FS.ReadFile("rosstat/population.sql")
	if err != nil {
		return nil, err
	}
	var stats ingest.LoadStats
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE pmo_stage (oktmo text, name text, year int, value bigint) ON COMMIT DROP`); err != nil {
			return err
		}
		rows := make([][]any, len(obs))
		for i, o := range obs {
			rows[i] = []any{o.OKTMO, o.Name, o.Year, o.Value}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"pmo_stage"}, []string{"oktmo", "name", "year", "value"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("stage population: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true)`,
			PopulationCode, env.Snapshot.ID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(script)); err != nil {
			return fmt.Errorf("transform population: %w", err)
		}
		stats, err = loadStats(ctx, tx)
		if err != nil {
			return err
		}
		// Почти все муниципальные образования старой Москвы совпадают с районами по названию;
		// поселения Новой Москвы объединены в районы и с ними не совпадают.
		matched := stats["areas_matched"]
		env.Check(ctx, ingest.Check{
			Code: "areas_matched", Title: "Муниципальные образования сопоставлены с районами (не меньше 120)",
			Severity: ingest.SeverityWarning, Passed: matched >= 120, Observed: matched,
			Expected: map[string]int64{"min": 120},
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

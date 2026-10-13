// Package calendar загружает производственный календарь РФ (рабочие, выходные и сокращённые
// дни с учётом переносов по постановлениям Правительства) из API isdayoff.ru.
package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/outbound"
)

// Code — код источника.
const Code = "prod_calendar"

const api = "https://isdayoff.ru/api/getdata"

// FirstYear — первый год календаря: с него есть данные наборов портала для сравнения.
const FirstYear = 2016

// kinds — коды API (с pre=1) и виды дня в ref.calendar_day.
var kinds = map[byte]string{'0': "workday", '1': "dayoff", '2': "short", '4': "nonworking_paid"}

// Year — коды дней года по порядку с 1 января.
type Year struct {
	Year int    `json:"year"`
	Days string `json:"days"`
}

// Day — день календаря.
type Day struct {
	Date time.Time
	Kind string
}

// ParseYear проверяет коды года и раскладывает их по датам.
func ParseYear(y Year) ([]Day, error) {
	first := time.Date(y.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	n := first.AddDate(1, 0, 0).Sub(first).Hours() / 24
	if len(y.Days) != int(n) {
		return nil, fmt.Errorf("календарь %d: %d кодов вместо %d", y.Year, len(y.Days), int(n))
	}
	out := make([]Day, len(y.Days))
	for i := range len(y.Days) {
		kind, ok := kinds[y.Days[i]]
		if !ok {
			return nil, fmt.Errorf("календарь %d: неизвестный код %q", y.Year, y.Days[i])
		}
		out[i] = Day{Date: first.AddDate(0, 0, i), Kind: kind}
	}
	return out, nil
}

// Source — загрузчик календаря.
type Source struct {
	// Now — текущее время (в тестах — фиксированное).
	Now func() time.Time
}

// New создаёт загрузчик.
func New() *Source { return &Source{Now: time.Now} }

// Descriptor описывает источник.
func (s *Source) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        Code,
		Title:       "Производственный календарь РФ",
		Publisher:   "isdayoff.ru — по постановлениям Правительства РФ о переносе выходных дней",
		URL:         "https://isdayoff.ru/",
		License:     "Сведения из официальных актов (не охраняются авторским правом, ст. 1259 ГК РФ)",
		Attribution: "Производственный календарь: isdayoff.ru",
		Domains:     []string{"reference"},
		Interval:    30 * 24 * time.Hour,
		Hosts:       []string{"isdayoff.ru"},
		Timeout:     5 * time.Minute,
	}
}

// Fetch запрашивает коды дней с FirstYear по следующий год.
func (s *Source) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	last := s.Now().Year() + 1
	var years []Year
	for y := FirstYear; y <= last; y++ {
		resp, err := env.HTTP.Get(ctx, api+"?cc=ru&pre=1&covid=1&year="+strconv.Itoa(y), outbound.Validators{})
		if err != nil {
			return ingest.Fetched{}, fmt.Errorf("календарь %d: %w", y, err)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if err != nil {
			return ingest.Fetched{}, err
		}
		year := Year{Year: y, Days: strings.TrimSpace(string(b))}
		if _, err := ParseYear(year); err != nil {
			// Следующий год бывает ещё не опубликован (ответ — код ошибки API).
			if y == last {
				env.Log.Info(ctx, fmt.Sprintf("календарь %d ещё не опубликован", y))
				break
			}
			return ingest.Fetched{}, err
		}
		years = append(years, year)
	}
	b, err := json.Marshal(years)
	if err != nil {
		return ingest.Fetched{}, err
	}
	obj, err := env.Store(ctx, "json", bytes.NewReader(b))
	if err != nil {
		return ingest.Fetched{}, err
	}
	n := int64(len(years))
	return ingest.Fetched{Object: obj, Format: "json", RecordCount: &n}, nil
}

// Load заменяет календарь годами снимка; годы после текущего — предварительные.
func (s *Source) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	f, err := env.Blobs.Open(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var years []Year
	if err := json.NewDecoder(f).Decode(&years); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	now := s.Now().Year()
	stats := ingest.LoadStats{"years": int64(len(years))}
	var rows [][]any
	for _, y := range years {
		days, err := ParseYear(y)
		if err != nil {
			return nil, err
		}
		for _, d := range days {
			rows = append(rows, []any{d.Date, d.Kind, y.Year > now, Code, env.Snapshot.ID})
			stats["days_"+d.Kind]++
		}
	}
	stats["days"] = int64(len(rows))
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM ref.calendar_day WHERE source_code = $1`, Code); err != nil {
			return err
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"ref", "calendar_day"},
			[]string{"day", "kind", "preliminary", "source_code", "snapshot_id"}, pgx.CopyFromRows(rows))
		return err
	})
	return stats, err
}

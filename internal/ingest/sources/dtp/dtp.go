// Package dtp загружает ДТП с пострадавшими по Москве из открытых данных «Карты ДТП»
// (dtp-stat.ru): первоисточник — статистика ГИБДД, координаты уточнены проектом. Сведения об
// участниках и конкретных ТС не загружаются.
package dtp

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/outbound"
)

// Code — код источника.
const Code = "dtp_moscow"

const dataURL = "https://dtp-stat.ru/media/opendata/moskva.geojson.zip"

// minIncidents — в Москве около 8 тыс. ДТП с пострадавшими в год, данные с 2015 г.
const minIncidents = 50000

// Incident — запись ДТП без сведений об участниках.
type Incident struct {
	ID             int64
	Datetime       string
	Category       string
	Severity       string
	Dead           int
	Injured        int
	Participants   int
	Light          string
	Weather        []string
	RoadConditions []string
	Nearby         []string
	Address        string
	District       string
	Vehicles       []string
	Lon, Lat       float64
}

// feature — объект GeoJSON выгрузки; поля участников и ТС, кроме категории, не читаются.
type feature struct {
	Geometry *struct {
		Coordinates []float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		ID                int64    `json:"id"`
		Datetime          string   `json:"datetime"`
		Category          string   `json:"category"`
		Severity          string   `json:"severity"`
		DeadCount         int      `json:"dead_count"`
		InjuredCount      int      `json:"injured_count"`
		ParticipantsCount int      `json:"participants_count"`
		Light             string   `json:"light"`
		Weather           []string `json:"weather"`
		RoadConditions    []string `json:"road_conditions"`
		Nearby            []string `json:"nearby"`
		Address           string   `json:"address"`
		Region            string   `json:"region"`
		Point             struct {
			Lat  float64 `json:"lat"`
			Long float64 `json:"long"`
		} `json:"point"`
		Vehicles []struct {
			Category *string `json:"category"`
		} `json:"vehicles"`
	} `json:"properties"`
}

// EachIncident читает FeatureCollection потоком и передаёт ДТП с координатами; возвращает
// число записей без координат.
func EachIncident(r io.Reader, fn func(Incident) error) (int, error) {
	dec := json.NewDecoder(r)
	if err := seekFeatures(dec); err != nil {
		return 0, err
	}
	skipped := 0
	for dec.More() {
		var f feature
		if err := dec.Decode(&f); err != nil {
			return skipped, fmt.Errorf("feature: %w", err)
		}
		p := f.Properties
		in := Incident{
			ID: p.ID, Datetime: p.Datetime, Category: p.Category, Severity: p.Severity, Dead: p.DeadCount,
			Injured: p.InjuredCount, Participants: p.ParticipantsCount, Light: p.Light, Weather: p.Weather,
			RoadConditions: p.RoadConditions, Nearby: p.Nearby, Address: p.Address, District: p.Region,
		}
		for _, v := range p.Vehicles {
			if v.Category != nil && *v.Category != "" {
				in.Vehicles = append(in.Vehicles, *v.Category)
			}
		}
		switch {
		case f.Geometry != nil && len(f.Geometry.Coordinates) == 2:
			in.Lon, in.Lat = f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
		case p.Point.Lat != 0 && p.Point.Long != 0:
			in.Lon, in.Lat = p.Point.Long, p.Point.Lat
		default:
			skipped++
			continue
		}
		if err := fn(in); err != nil {
			return skipped, err
		}
	}
	return skipped, nil
}

// seekFeatures продвигает декодер к первому элементу массива features.
func seekFeatures(dec *json.Decoder) error {
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("geojson: expected object, got %v (%w)", t, err)
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if t == "features" {
			if t, err := dec.Token(); err != nil || t != json.Delim('[') {
				return fmt.Errorf("geojson: features is not an array (%w)", err)
			}
			return nil
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
	return errors.New("geojson: no features")
}

// Source — загрузчик ДТП.
type Source struct{}

// New создаёт загрузчик.
func New() *Source { return &Source{} }

// Descriptor описывает источник.
func (s *Source) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        Code,
		Title:       "ДТП с пострадавшими в Москве",
		Publisher:   "«Карта ДТП» (dtp-stat.ru) по данным ГИБДД",
		URL:         "https://dtp-stat.ru/opendata/",
		License:     "Использование с указанием активной ссылки на dtp-stat.ru; первоисточник — статистика ГИБДД",
		Attribution: "«Карта ДТП», dtp-stat.ru (данные ГИБДД)",
		Domains:     []string{"operations"},
		Interval:    30 * 24 * time.Hour,
		Hosts:       []string{"dtp-stat.ru"},
		Timeout:     20 * time.Minute,
		// Архив около 15 МБ.
		RequestTimeout: 5 * time.Minute,
	}
}

// Fetch скачивает архив, если он изменился (условный запрос по ETag и Last-Modified).
func (s *Source) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	var prev outbound.Validators
	if env.Previous != nil {
		prev = env.Previous.Validators
	}
	resp, err := env.HTTP.Get(ctx, dataURL, prev)
	if errors.Is(err, outbound.ErrNotModified) {
		return ingest.Fetched{NotModified: true}, nil
	}
	if err != nil {
		return ingest.Fetched{}, err
	}
	defer resp.Body.Close()
	obj, err := env.Store(ctx, "zip", resp.Body)
	if err != nil {
		return ingest.Fetched{}, err
	}
	return ingest.Fetched{Object: obj, Format: "zip", Version: resp.Validators.LastModified, Validators: resp.Validators}, nil
}

// Load переносит ДТП в operations.incident (etl/dtp/incidents.sql).
func (s *Source) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	path, err := env.Blobs.LocalPath(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer zr.Close()
	var data io.ReadCloser
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".geojson") {
			if data, err = f.Open(); err != nil {
				return nil, err
			}
			break
		}
	}
	if data == nil {
		return nil, errors.New("archive has no .geojson")
	}
	defer data.Close()

	script, err := etl.FS.ReadFile("dtp/incidents.sql")
	if err != nil {
		return nil, err
	}
	var stats ingest.LoadStats
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE dtp_stage (
			source_id bigint, datetime text, category text, severity text, dead int, injured int, participants int,
			light text, weather text[], road_conditions text[], nearby text[], address text, district text,
			vehicles text[], lon float8, lat float8) ON COMMIT DROP`); err != nil {
			return err
		}
		var rows [][]any
		skipped, err := EachIncident(data, func(in Incident) error {
			rows = append(rows, []any{
				in.ID, in.Datetime, in.Category, in.Severity, in.Dead, in.Injured, in.Participants,
				in.Light, in.Weather, in.RoadConditions, in.Nearby, in.Address, in.District, in.Vehicles, in.Lon, in.Lat,
			})
			return nil
		})
		if err != nil {
			return err
		}
		n := int64(len(rows))
		env.Check(ctx, ingest.Check{
			Code: "incidents", Title: fmt.Sprintf("ДТП в выгрузке не меньше %d", minIncidents), Severity: ingest.SeverityCritical,
			Passed: n >= minIncidents, Observed: n, Expected: map[string]int64{"min": minIncidents},
		})
		if n < minIncidents {
			return ingest.ErrQualityFailed
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"dtp_stage"}, []string{
			"source_id", "datetime", "category", "severity",
			"dead", "injured", "participants", "light", "weather", "road_conditions", "nearby", "address", "district",
			"vehicles", "lon", "lat",
		}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("stage incidents: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true)`,
			Code, env.Snapshot.ID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(script)); err != nil {
			return fmt.Errorf("transform incidents: %w", err)
		}
		if stats, err = loadStats(ctx, tx); err != nil {
			return err
		}
		stats["incidents_without_point"] = int64(skipped)
		return nil
	})
	return stats, err
}

func loadStats(ctx context.Context, tx pgx.Tx) (ingest.LoadStats, error) {
	rows, err := tx.Query(ctx, `SELECT key, coalesce(value, 0) FROM load_stats`)
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

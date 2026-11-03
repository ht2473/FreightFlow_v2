package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// territoryClause — условие «геометрия g в границе территории $1 (или весь город, если $1 NULL)».
// Граница — скалярный подзапрос (вычисляется один раз), && использует индекс геометрии строк.
const territoryClause = `($1::uuid IS NULL OR %[1]s && (SELECT geom FROM territory.admin_area WHERE id = $1)
	AND ST_Intersects(%[1]s, (SELECT geom FROM territory.admin_area WHERE id = $1)))`

func inTerritory(geom string) string { return fmt.Sprintf(territoryClause, geom) }

// TerritoryRef — территория отчёта.
type TerritoryRef struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind"`
	Name string    `json:"name"`
	// BBox — охват [запад, юг, восток, север].
	BBox [4]float64 `json:"bbox"`
}

func (s *Service) territory(ctx context.Context, id *uuid.UUID) (*TerritoryRef, error) {
	if id == nil {
		return nil, nil
	}
	var t TerritoryRef
	err := s.pool.QueryRow(ctx, `SELECT id, kind, name, ST_XMin(geom), ST_YMin(geom), ST_XMax(geom), ST_YMax(geom)
		FROM territory.admin_area WHERE id = $1`, *id).Scan(&t.ID, &t.Kind, &t.Name, &t.BBox[0], &t.BBox[1], &t.BBox[2], &t.BBox[3])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// RestrictionKind — ограничения одного вида на территории.
type RestrictionKind struct {
	Kind      string  `json:"kind"`
	Count     int     `json:"count"`
	LengthKm  float64 `json:"lengthKm"`
	Blocking  int     `json:"blocking"` // не пропускают профиль
	BlockedKm float64 `json:"blockedKm"`
}

// Blocker — участок, который профиль не проходит.
type Blocker struct {
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Value    *float64        `json:"value,omitempty"`
	Unit     string          `json:"unit,omitempty"`
	LengthM  float64         `json:"lengthM"`
	Geometry json.RawMessage `json:"geometry"`
}

// Passability — проходимость территории для профиля ТС.
type Passability struct {
	Territory *TerritoryRef     `json:"territory,omitempty"`
	Profile   string            `json:"profile"`
	Name      string            `json:"profileName"`
	Kinds     []RestrictionKind `json:"kinds"`
	Blockers  []Blocker         `json:"blockers"`
	NetworkKm float64           `json:"networkKm"`
	BlockedKm float64           `json:"blockedKm"`
}

// blocks — ограничение не пропускает профиль p (запрет или предел меньше габарита).
const blocks = `(r.kind IN ('hgv_ban', 'motor_vehicle_ban')
	OR r.kind = 'hazmat_ban' AND p.hazmat
	OR r.kind = 'maxheight' AND r.value < p.height_m
	OR r.kind = 'maxweight' AND r.value < p.gross_t
	OR r.kind = 'maxaxleload' AND r.value < p.axle_load_t
	OR r.kind = 'maxwidth' AND r.value < p.width_m
	OR r.kind = 'maxlength' AND r.value < p.length_m)`

// PassabilityData собирает ограничения на территории (или в городе) для профиля ТС.
func (s *Service) PassabilityData(ctx context.Context, territoryID *uuid.UUID, profile string) (*Passability, error) {
	out := &Passability{Profile: profile, Kinds: []RestrictionKind{}, Blockers: []Blocker{}}
	err := s.pool.QueryRow(ctx, `SELECT name FROM ref.vehicle_profile WHERE code = $1`, profile).Scan(&out.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, invalid("неизвестный профиль ТС %q", profile)
	}
	if err != nil {
		return nil, err
	}
	if out.Territory, err = s.territory(ctx, territoryID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.kind, count(*), coalesce(sum(sg.length_m), 0) / 1000,
			count(*) FILTER (WHERE `+blocks+`), coalesce(sum(sg.length_m) FILTER (WHERE `+blocks+`), 0) / 1000
		FROM network.restriction r
		JOIN network.road_segment sg ON sg.id = r.segment_id
		JOIN ref.vehicle_profile p ON p.code = $2
		WHERE `+inTerritory("sg.geom")+`
		GROUP BY r.kind ORDER BY count(*) DESC`, territoryID, profile)
	if err != nil {
		return nil, fmt.Errorf("restrictions by kind: %w", err)
	}
	if out.Kinds, err = pgx.CollectRows(rows, pgx.RowToStructByPos[RestrictionKind]); err != nil {
		return nil, err
	}
	rows, err = s.pool.Query(ctx, `
		SELECT coalesce(nullif(sg.name, ''), 'Участок без названия'), r.kind, r.value::float8, coalesce(r.unit, ''),
			sg.length_m, ST_AsGeoJSON(sg.geom, 5)::json
		FROM network.restriction r
		JOIN network.road_segment sg ON sg.id = r.segment_id
		JOIN ref.vehicle_profile p ON p.code = $2
		WHERE `+inTerritory("sg.geom")+` AND `+blocks+`
		ORDER BY sg.class IN ('motorway', 'trunk', 'primary', 'secondary') DESC, sg.length_m DESC LIMIT 300`, territoryID, profile)
	if err != nil {
		return nil, fmt.Errorf("blockers: %w", err)
	}
	if out.Blockers, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Blocker]); err != nil {
		return nil, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT coalesce(sum(sg.length_m), 0) / 1000,
			coalesce(sum(sg.length_m) FILTER (WHERE EXISTS (SELECT 1 FROM network.restriction r JOIN ref.vehicle_profile p
				ON p.code = $2 WHERE r.segment_id = sg.id AND `+blocks+`)), 0) / 1000
		FROM network.road_segment sg WHERE `+inTerritory("sg.geom"), territoryID, profile).Scan(&out.NetworkKm, &out.BlockedKm)
	return out, err
}

// Work — дорожная работа в отчёте.
type Work struct {
	ID         uuid.UUID       `json:"id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Place      string          `json:"place"`
	District   string          `json:"district"`
	State      string          `json:"state"`
	Closure    bool            `json:"closure"`
	Start      *time.Time      `json:"start,omitempty"`
	End        *time.Time      `json:"end,omitempty"`
	Facilities int             `json:"facilities"`
	ImpactKm   float64         `json:"impactKm"`
	Geometry   json.RawMessage `json:"geometry,omitempty"`
}

// Roadworks — дорожные работы на территории: идущие и начинающиеся в ближайшие дни.
type Roadworks struct {
	Territory *TerritoryRef  `json:"territory,omitempty"`
	Days      int            `json:"days"`
	ByKind    map[string]int `json:"byKind"`
	Active    int            `json:"active"`
	Planned   int            `json:"planned"`
	Closures  int            `json:"closures"`
	Works     []Work         `json:"works"`
	Series    []SeriesPoint  `json:"series"` // идущие работы по дням (город)
}

// SeriesPoint — значение ряда за день.
type SeriesPoint struct {
	Day   time.Time `json:"day"`
	Value float64   `json:"value"`
}

// RoadworksData собирает работы: идущие и те, что начнутся в ближайшие days дней.
func (s *Service) RoadworksData(ctx context.Context, territoryID *uuid.UUID, days int) (*Roadworks, error) {
	out := &Roadworks{Days: days, ByKind: map[string]int{}, Works: []Work{}, Series: []SeriesPoint{}}
	var err error
	if out.Territory, err = s.territory(ctx, territoryID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.kind, w.title, coalesce(w.place, ''), coalesce(w.district, ''), w.state, w.closure,
			coalesce(w.actual_start, w.planned_start)::timestamptz, coalesce(w.actual_end, w.planned_end)::timestamptz,
			(SELECT count(*) FROM operations.road_work_facility f WHERE f.work_id = w.id)::int,
			coalesce((SELECT sum(i.length_m) FROM operations.road_work_impact i WHERE i.work_id = w.id), 0) / 1000,
			ST_AsGeoJSON(w.geom, 5)::json
		FROM operations.road_work w
		WHERE w.geom IS NOT NULL AND `+inTerritory("w.geom")+`
		  AND (w.state = 'active' OR w.state = 'planned' AND coalesce(w.planned_start, current_date) <= current_date + $2::int)
		ORDER BY w.state = 'active' DESC, w.closure DESC, w.kind = 'emergency' DESC, 10 DESC, w.planned_start
		LIMIT 500`, territoryID, days)
	if err != nil {
		return nil, fmt.Errorf("road works: %w", err)
	}
	if out.Works, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Work]); err != nil {
		return nil, err
	}
	for _, w := range out.Works {
		out.ByKind[w.Kind]++
		if w.State == "active" {
			out.Active++
		} else {
			out.Planned++
		}
		if w.Closure {
			out.Closures++
		}
	}
	rows, err = s.pool.Query(ctx, `SELECT day::timestamptz, value::float8 FROM operations.daily_snapshot
		WHERE metric = 'works_active' AND scope = 'city' AND day > current_date - $1::int ORDER BY day`, max(days, 30))
	if err != nil {
		return nil, err
	}
	out.Series, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SeriesPoint])
	return out, err
}

// SourceQuality — состояние источника данных.
type SourceQuality struct {
	Code        string     `json:"code"`
	Title       string     `json:"title"`
	License     string     `json:"license"`
	IntervalS   *int64     `json:"intervalS,omitempty"`
	LastStatus  *string    `json:"lastStatus,omitempty"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
	LastSuccess *time.Time `json:"lastSuccess,omitempty"`
	Stale       bool       `json:"stale"`
	FailedRuns  int        `json:"failedRuns30d"`
	ChecksFail  int        `json:"checksFailed"` // проверок не пройдено в последнем запуске
}

// Quality — качество данных: источники и полнота реестра.
type Quality struct {
	Sources    []SourceQuality `json:"sources"`
	Facilities int             `json:"facilities"`
	Address    int             `json:"withAddress"`
	Area       int             `json:"withFloorArea"`
	Confidence float64         `json:"avgConfidence"`
	Bands      map[string]int  `json:"confidenceBands"` // high ≥ 0,7, medium ≥ 0,4, low
}

// QualityData собирает свежесть источников, неудачи загрузок и полноту реестра.
func (s *Service) QualityData(ctx context.Context) (*Quality, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.code, s.title, s.license, s.interval_s, lr.status, lr.finished_at, ok.finished_at,
			ok.finished_at IS NOT NULL AND s.interval_s IS NOT NULL AND ok.finished_at < now() - make_interval(secs => s.interval_s * 2),
			(SELECT count(*) FROM ingest.run f WHERE f.source_code = s.code AND f.status = 'failed'
				AND f.finished_at > now() - interval '30 days')::int,
			coalesce((SELECT count(*) FROM ingest.quality_check q WHERE q.run_id = lr.id AND NOT q.passed), 0)::int
		FROM ingest.source s
		LEFT JOIN LATERAL (SELECT id, status, finished_at FROM ingest.run WHERE source_code = s.code
			ORDER BY coalesce(finished_at, queued_at) DESC LIMIT 1) lr ON true
		LEFT JOIN LATERAL (SELECT finished_at FROM ingest.run WHERE source_code = s.code
			AND status IN ('succeeded', 'unchanged') ORDER BY finished_at DESC LIMIT 1) ok ON true
		WHERE s.enabled ORDER BY s.title`)
	if err != nil {
		return nil, fmt.Errorf("source quality: %w", err)
	}
	out := &Quality{Bands: map[string]int{}}
	if out.Sources, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SourceQuality]); err != nil {
		return nil, err
	}
	var high, medium, low int
	err = s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE coalesce(official_address, address) IS NOT NULL),
			count(*) FILTER (WHERE floor_area_m2 IS NOT NULL), coalesce(avg(confidence), 0)::float8,
			count(*) FILTER (WHERE confidence >= 0.7), count(*) FILTER (WHERE confidence >= 0.4 AND confidence < 0.7),
			count(*) FILTER (WHERE confidence < 0.4)
		FROM infra.facility WHERE status = 'active'`).
		Scan(&out.Facilities, &out.Address, &out.Area, &out.Confidence, &high, &medium, &low)
	out.Bands["high"], out.Bands["medium"], out.Bands["low"] = high, medium, low
	return out, err
}

// Package regulation — регулирование грузового движения: зоны ограничения и правила с
// правовыми основаниями, официальные страницы для сверки, грузовой каркас, статистика пропусков
// и спецразрешений. Данные загружают источники модуля ingest; модуль их читает.
package regulation

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service читает сведения о регулировании.
type Service struct {
	pool *pgxpool.Pool
}

// New создаёт сервис.
func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Zone — зона внутри кольца.
type Zone struct {
	Code, Name string
	LengthKm   float64
	AreaKm2    *float64
}

// Rule — правило зоны (regulation.zone_rule).
type Rule struct {
	Code, Zone, Title, Effect string
	MassBasis                 *string
	MassOverT                 *float64
	EcoClassMin               *int
	TimeFrom, TimeTo          *string
	PassKind                  *string
	PassZones                 []string
	ValidFrom, ValidTo        *time.Time
	Document                  string
	Clause                    *string
	Confirmed                 bool
	ConfirmedBy               *string
	Note                      *string
}

// Document — правовой акт или официальная страница (ref.legal_document).
type Document struct {
	Code, Kind, Title, Authority, Status string
	Number, URL, Amends, Summary         *string
	SignedOn, EffectiveOn, VerifiedOn    *time.Time
}

// Page — официальная страница, по которой сверены правила.
type Page struct {
	Code, Title, URL         string
	FetchedAt, ChangedAt     time.Time
	ChangedSinceVerification bool
}

// FrameOkrug — грузовой каркас в округе.
type FrameOkrug struct {
	Okrug            string
	Streets, Matched int
	LengthKm         float64
}

// Frame — сводка грузового каркаса.
type Frame struct {
	Streets, Matched, Partial int
	LengthKm                  float64
	UpdatedAt                 *time.Time
	Okrugs                    []FrameOkrug
}

// Overview — зоны, правила, документы, страницы и грузовой каркас.
type Overview struct {
	Zones     []Zone
	Rules     []Rule
	Documents []Document
	Pages     []Page
	Frame     Frame
}

// Overview собирает сводку.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	var err error
	if o.Zones, err = collect(ctx, s.pool, `
		SELECT code, name, length_km, area_km2 FROM territory.ring_zone WHERE code IN ('sk', 'ttk', 'mkad') ORDER BY ord`,
		func(r pgx.Rows) (Zone, error) {
			var z Zone
			return z, r.Scan(&z.Code, &z.Name, &z.LengthKm, &z.AreaKm2)
		}); err != nil {
		return o, fmt.Errorf("zones: %w", err)
	}
	if o.Rules, err = collect(ctx, s.pool, `
		SELECT code, zone, title, effect, mass_basis, mass_over_t::float8, eco_class_min,
		       to_char(time_from, 'HH24:MI'), to_char(time_to, 'HH24:MI'), pass_kind, coalesce(pass_zones, '{}'),
		       valid_from, valid_to, document_code, clause, confirmed, confirmed_by, note
		FROM regulation.zone_rule ORDER BY ord, code`,
		func(r pgx.Rows) (Rule, error) {
			var x Rule
			return x, r.Scan(&x.Code, &x.Zone, &x.Title, &x.Effect, &x.MassBasis, &x.MassOverT, &x.EcoClassMin,
				&x.TimeFrom, &x.TimeTo, &x.PassKind, &x.PassZones, &x.ValidFrom, &x.ValidTo, &x.Document, &x.Clause,
				&x.Confirmed, &x.ConfirmedBy, &x.Note)
		}); err != nil {
		return o, fmt.Errorf("rules: %w", err)
	}
	if o.Documents, err = collect(ctx, s.pool, `
		SELECT code, kind, title, authority, status, number, url, amends, summary, signed_on, effective_on, verified_on
		FROM ref.legal_document
		ORDER BY CASE kind WHEN 'act' THEN 1 WHEN 'amendment' THEN 2 ELSE 3 END, signed_on NULLS LAST, code`,
		func(r pgx.Rows) (Document, error) {
			var d Document
			return d, r.Scan(&d.Code, &d.Kind, &d.Title, &d.Authority, &d.Status, &d.Number, &d.URL, &d.Amends, &d.Summary,
				&d.SignedOn, &d.EffectiveOn, &d.VerifiedOn)
		}); err != nil {
		return o, fmt.Errorf("documents: %w", err)
	}
	if o.Pages, err = collect(ctx, s.pool, `
		SELECT p.code, p.title, coalesce(d.url, ''), p.fetched_at, p.changed_at, p.verified_hash IS DISTINCT FROM p.content_hash
		FROM regulation.official_page p JOIN ref.legal_document d USING (code) ORDER BY p.code`,
		func(r pgx.Rows) (Page, error) {
			var p Page
			return p, r.Scan(&p.Code, &p.Title, &p.URL, &p.FetchedAt, &p.ChangedAt, &p.ChangedSinceVerification)
		}); err != nil {
		return o, fmt.Errorf("pages: %w", err)
	}
	if o.Frame.Okrugs, err = collect(ctx, s.pool, `
		SELECT okrug, count(*), count(*) FILTER (WHERE segments > 0), coalesce(sum(length_km), 0)
		FROM regulation.cargo_frame_street GROUP BY okrug ORDER BY okrug`,
		func(r pgx.Rows) (FrameOkrug, error) {
			var k FrameOkrug
			return k, r.Scan(&k.Okrug, &k.Streets, &k.Matched, &k.LengthKm)
		}); err != nil {
		return o, fmt.Errorf("cargo frame: %w", err)
	}
	// Длина каркаса — по участкам сети без повторов (улица может входить в перечень двух округов).
	err = s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE segments > 0), count(*) FILTER (WHERE extent IS NOT NULL),
		       coalesce((SELECT sum(r.length_m) / 1000 FROM network.road_segment r
		                 WHERE r.id IN (SELECT segment_id FROM regulation.cargo_frame_segment)), 0),
		       (SELECT max(fetched_at) FROM regulation.official_page WHERE code = 'tm-cargo-frame-streets')
		FROM regulation.cargo_frame_street`).
		Scan(&o.Frame.Streets, &o.Frame.Matched, &o.Frame.Partial, &o.Frame.LengthKm, &o.Frame.UpdatedAt)
	if err != nil {
		return o, fmt.Errorf("cargo frame: %w", err)
	}
	return o, nil
}

// MonthValue — значение за месяц.
type MonthValue struct {
	Month time.Time
	Value int
}

// PassSeries — ряд пропусков одной зоны, серии и показателя.
type PassSeries struct {
	Zone, Series, Metric string
	Points               []MonthValue
}

// PassStats возвращает ряды пропусков.
func (s *Service) PassStats(ctx context.Context) ([]PassSeries, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT zone, series, metric, month, value FROM regulation.pass_stat ORDER BY zone, series, metric, month`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PassSeries{}
	for rows.Next() {
		var zone, series, metric string
		var p MonthValue
		if err := rows.Scan(&zone, &series, &metric, &p.Month, &p.Value); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].Zone != zone || out[n-1].Series != series || out[n-1].Metric != metric {
			out = append(out, PassSeries{Zone: zone, Series: series, Metric: metric})
		}
		out[len(out)-1].Points = append(out[len(out)-1].Points, p)
	}
	return out, rows.Err()
}

// NamedCount — число записей вида.
type NamedCount struct {
	Name  string
	Count int
}

// PermitStats — спецразрешения: всего, по месяцам выдачи и по превышенным параметрам.
type PermitStats struct {
	Total    int
	Months   []MonthValue
	Exceeded []NamedCount
}

// PermitStats возвращает статистику спецразрешений.
func (s *Service) PermitStats(ctx context.Context) (PermitStats, error) {
	var st PermitStats
	var err error
	if st.Months, err = collect(ctx, s.pool, `
		SELECT date_trunc('month', issued_on)::date, count(*) FROM regulation.special_permit
		WHERE issued_on IS NOT NULL GROUP BY 1 ORDER BY 1`,
		func(r pgx.Rows) (MonthValue, error) {
			var m MonthValue
			return m, r.Scan(&m.Month, &m.Value)
		}); err != nil {
		return st, err
	}
	// Пороги — предельные габариты (ширина 2,55 м, высота 4 м, длина автопоезда 20 м) и масса
	// 40 т; допустимая масса на деле зависит от числа осей, поэтому разрез — ориентировочный.
	var mass, width, height, length int
	err = s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE mass_loaded_t > 40), count(*) FILTER (WHERE width_m > 2.55),
		       count(*) FILTER (WHERE height_m > 4), count(*) FILTER (WHERE length_m > 20)
		FROM regulation.special_permit`).Scan(&st.Total, &mass, &width, &height, &length)
	st.Exceeded = []NamedCount{
		{Name: "Масса с грузом более 40 т", Count: mass},
		{Name: "Ширина более 2,55 м", Count: width},
		{Name: "Высота более 4 м", Count: height},
		{Name: "Длина более 20 м", Count: length},
	}
	return st, err
}

func collect[T any](ctx context.Context, pool *pgxpool.Pool, q string, scan func(pgx.Rows) (T, error)) ([]T, error) {
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SignType — знаки ограничений одного вида и их привязка к сети.
type SignType struct {
	Code, Name, Kind                     string
	Count, Matched, Ambiguous, Unmatched int
}

// Conflict — число участков с расхождением знаков и OSM одного типа и вида ограничения.
type Conflict struct {
	Type, Kind string
	Segments   int
}

// SignQuality — отчёт качества знаков ограничений.
type SignQuality struct {
	Active, RestrictionSigns, Matched, Ambiguous, Unmatched, Restrictions int
	Types                                                                 []SignType
	Conflicts                                                             []Conflict
}

// SignQuality собирает отчёт по действующим знакам реестра.
func (s *Service) SignQuality(ctx context.Context) (SignQuality, error) {
	var q SignQuality
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), count(t.restriction_kind),
		       count(*) FILTER (WHERE t.restriction_kind IS NOT NULL AND s.match_status = 'matched'),
		       count(*) FILTER (WHERE t.restriction_kind IS NOT NULL AND s.match_status = 'ambiguous'),
		       count(*) FILTER (WHERE t.restriction_kind IS NOT NULL AND s.match_status = 'unmatched'),
		       (SELECT count(*) FROM network.restriction WHERE origin = 'sign')
		FROM network.road_sign s LEFT JOIN ref.road_sign_type t USING (code)
		WHERE s.status = 'active'`).
		Scan(&q.Active, &q.RestrictionSigns, &q.Matched, &q.Ambiguous, &q.Unmatched, &q.Restrictions)
	if err != nil {
		return q, fmt.Errorf("signs: %w", err)
	}
	if q.Types, err = collect(ctx, s.pool, `
		SELECT t.code, t.name, t.restriction_kind, count(*),
		       count(*) FILTER (WHERE s.match_status = 'matched'), count(*) FILTER (WHERE s.match_status = 'ambiguous'),
		       count(*) FILTER (WHERE s.match_status = 'unmatched')
		FROM network.road_sign s JOIN ref.road_sign_type t USING (code)
		WHERE s.status = 'active' AND t.restriction_kind IS NOT NULL
		GROUP BY t.code, t.name, t.restriction_kind ORDER BY count(*) DESC, t.code`,
		func(r pgx.Rows) (SignType, error) {
			var x SignType
			return x, r.Scan(&x.Code, &x.Name, &x.Kind, &x.Count, &x.Matched, &x.Ambiguous, &x.Unmatched)
		}); err != nil {
		return q, fmt.Errorf("sign types: %w", err)
	}
	if q.Conflicts, err = collect(ctx, s.pool, `
		SELECT type, kind, count(DISTINCT segment_id) FROM network.restriction_conflict
		GROUP BY type, kind ORDER BY type DESC, count(DISTINCT segment_id) DESC`,
		func(r pgx.Rows) (Conflict, error) {
			var c Conflict
			return c, r.Scan(&c.Type, &c.Kind, &c.Segments)
		}); err != nil {
		return q, fmt.Errorf("conflicts: %w", err)
	}
	return q, nil
}

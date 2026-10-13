package routing

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Moscow — часовой пояс правил зон.
var Moscow = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}()

// Vehicle — параметры ТС для политики зон: разрешённая максимальная масса и грузоподъёмность в
// тоннах, экологический класс (0 — не указан), опасный груз.
type Vehicle struct {
	GrossT   float64
	PayloadT float64
	EcoClass int
	Hazmat   bool
}

// Pass — пропуск: зона действия (sk, ttk, mkad) и время действия (day, night).
type Pass struct {
	Zone string
	Kind string
}

// ZoneRule — правило зоны (regulation.zone_rule).
type ZoneRule struct {
	Code, Zone, Title, Effect string
	MassBasis                 string
	MassOverT                 float64
	EcoClassMin               int
	// From, To — минуты от полуночи; From = To = -1 — круглосуточно.
	From, To  int
	PassKind  string
	PassZones []string
	ValidFrom time.Time
	ValidTo   time.Time
	Clause    string
	Document  string
	Confirmed bool
}

// Applied — сработавшее правило: что требуется и почему.
type Applied struct {
	Rule ZoneRule
	// Blocking — правило закрывает зону для этого ТС (нет нужного пропуска или запрет).
	Blocking bool
	Note     string
}

// ZoneDecision — решение по зоне.
type ZoneDecision struct {
	Zone      string
	Forbidden bool
	Rules     []Applied
	// Unconfirmed — решение опирается на неподтверждённое правило.
	Unconfirmed bool
}

// Decide применяет правила к ТС, пропускам и времени отправления (по Москве). Зона закрыта, если
// хотя бы одно действующее правило её закрывает. Неизвестная грузоподъёмность считается
// превышающей порог у грузовиков более 2,5 т; неизвестный экологический класс правило не
// включает, но отмечается.
func Decide(rules []ZoneRule, v Vehicle, passes []Pass, at time.Time) []ZoneDecision {
	at = at.In(Moscow)
	minute := at.Hour()*60 + at.Minute()
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	out := []ZoneDecision{}
	for _, zone := range []string{"sk", "ttk", "mkad"} {
		d := ZoneDecision{Zone: zone}
		confirmed := false
		for _, r := range rules {
			if r.Zone != zone || !activeOn(r, day) || !inHours(r, minute) {
				continue
			}
			subject, note := applies(r, v)
			if !subject {
				continue
			}
			a := Applied{Rule: r, Note: note}
			switch r.Effect {
			case "prohibited":
				a.Blocking = true
			case "pass_required":
				a.Blocking = !hasPass(passes, r)
			case "frame_only":
				// Ограничение жилых зон — знаками на улицах, зона целиком не закрывается.
			}
			if note == "eco_unknown" {
				// Класс не указан: правило показывается, но зону не закрывает.
				a.Blocking = false
			}
			d.Rules = append(d.Rules, a)
			if a.Blocking {
				d.Forbidden = true
				confirmed = confirmed || r.Confirmed
			}
		}
		// Решение неподтверждено, если зону закрывают только неподтверждённые правила.
		d.Unconfirmed = d.Forbidden && !confirmed
		out = append(out, d)
	}
	return out
}

func activeOn(r ZoneRule, day time.Time) bool {
	if !r.ValidFrom.IsZero() && day.Before(r.ValidFrom) {
		return false
	}
	return r.ValidTo.IsZero() || !day.After(r.ValidTo)
}

// inHours — время отправления в интервале правила; интервал через полночь (23:00–07:00) — с
// переходом.
func inHours(r ZoneRule, minute int) bool {
	if r.From < 0 || r.To < 0 {
		return true
	}
	if r.From <= r.To {
		return minute >= r.From && minute < r.To
	}
	return minute >= r.From || minute < r.To
}

func applies(r ZoneRule, v Vehicle) (bool, string) {
	if r.EcoClassMin > 0 {
		if v.EcoClass == 0 {
			return true, "eco_unknown"
		}
		return v.EcoClass < r.EcoClassMin, ""
	}
	if r.MassBasis == "payload" {
		if v.PayloadT == 0 {
			return v.GrossT > 2.5, "payload_unknown"
		}
		return v.PayloadT > r.MassOverT, ""
	}
	return v.GrossT > r.MassOverT, ""
}

func hasPass(passes []Pass, r ZoneRule) bool {
	for _, p := range passes {
		if p.Kind == r.PassKind && slices.Contains(r.PassZones, p.Zone) {
			return true
		}
	}
	return false
}

// LoadRules читает правила зон.
func LoadRules(ctx context.Context, pool *pgxpool.Pool) ([]ZoneRule, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.code, r.zone, r.title, r.effect, coalesce(r.mass_basis, ''), coalesce(r.mass_over_t, 0)::float8,
		       coalesce(r.eco_class_min, 0), coalesce(extract(epoch FROM r.time_from)::int / 60, -1),
		       coalesce(extract(epoch FROM r.time_to)::int / 60, -1), coalesce(r.pass_kind, ''),
		       coalesce(r.pass_zones, '{}'), coalesce(r.valid_from, '0001-01-01'), coalesce(r.valid_to, '0001-01-01'),
		       coalesce(r.clause, ''), coalesce(d.number, d.title), r.confirmed
		FROM regulation.zone_rule r JOIN ref.legal_document d ON d.code = r.document_code
		ORDER BY r.ord, r.code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ZoneRule, error) {
		var r ZoneRule
		err := row.Scan(&r.Code, &r.Zone, &r.Title, &r.Effect, &r.MassBasis, &r.MassOverT, &r.EcoClassMin, &r.From, &r.To,
			&r.PassKind, &r.PassZones, &r.ValidFrom, &r.ValidTo, &r.Clause, &r.Document, &r.Confirmed)
		if r.ValidFrom.Year() == 1 {
			r.ValidFrom = time.Time{}
		}
		if r.ValidTo.Year() == 1 {
			r.ValidTo = time.Time{}
		}
		return r, err
	})
}

// ZoneRings возвращает контуры зон для исключения из маршрута: зона расширена на 40 м, чтобы
// исключались обе проезжие части кольца (граница зоны — по оси дороги, а движение по кольцу
// тоже требует пропуска), и упрощена до ~30 м — служба ограничивает длину многоугольников.
func ZoneRings(ctx context.Context, pool *pgxpool.Pool, zones []string) (map[string][]Ring, error) {
	rows, err := pool.Query(ctx, `
		WITH z AS (
		    SELECT code, (ST_DumpRings((ST_Dump(ST_SimplifyPreserveTopology(
		               ST_Buffer(zone::geography, 40)::geometry, 0.0003))).geom)) AS r
		    FROM territory.ring_zone WHERE code = ANY ($1) AND zone IS NOT NULL
		)
		SELECT code, (r).path[1], ST_AsGeoJSON((r).geom)::json->'coordinates'->0 FROM z`, zones)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Ring{}
	for rows.Next() {
		var code string
		var idx int
		var ring Ring
		if err := rows.Scan(&code, &idx, &ring); err != nil {
			return nil, fmt.Errorf("zone ring: %w", err)
		}
		// Внутренние кольца (дыры) не исключаются отдельно.
		if idx == 0 {
			out[code] = append(out[code], ring)
		}
	}
	return out, rows.Err()
}

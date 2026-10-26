package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
	"freightflow/internal/routing"
)

// newEntity — идентификатор гипотетического объекта в результатах.
const newEntity = "new"

// progressEvery — не чаще одного события хода расчёта за этот интервал.
const progressEvery = time.Second

// entity — объект расчёта: реестр или новый.
type entity struct {
	key   string
	point routing.Point
}

// variant — база или сценарий: решения по зонам, исключения, точки в закрытой зоне.
type variant struct {
	zones   []routing.ZoneDecision
	ex      routing.Exclusions
	blocked map[int]bool
}

// tracker — ход расчёта: сохраняется в сценарии и рассылается событием job.progress.
type tracker struct {
	s      *Service
	id     uuid.UUID
	userID uuid.UUID
	mu     sync.Mutex
	p      Progress
	sent   time.Time
}

func (t *tracker) stage(ctx context.Context, stage string) {
	t.mu.Lock()
	t.p.Stage = stage
	t.mu.Unlock()
	t.flush(ctx, true)
}

func (t *tracker) add(ctx context.Context, n int) {
	t.mu.Lock()
	t.p.Done += n
	t.mu.Unlock()
	t.flush(ctx, false)
}

func (t *tracker) flush(ctx context.Context, force bool) {
	t.mu.Lock()
	if !force && time.Since(t.sent) < progressEvery {
		t.mu.Unlock()
		return
	}
	t.sent = time.Now()
	p := t.p
	t.mu.Unlock()
	b, _ := json.Marshal(p)
	// Ход расчёта — подсказка интерфейсу: ошибка записи не прерывает расчёт.
	_ = db.InTx(ctx, t.s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE scenario.scenario SET progress = $2 WHERE id = $1`, t.id, b); err != nil {
			return err
		}
		return events.Publish(ctx, tx, events.JobProgress, map[string]any{
			"userId": t.userID, "kind": "scenario", "id": t.id, "stage": p.Stage, "done": p.Done, "total": p.Total,
		})
	})
}

// UserMessage — причина неудачи для пользователя.
func UserMessage(err error) string {
	var inv *ErrInvalid
	switch {
	case errors.As(err, &inv):
		return inv.Reason
	case errors.Is(err, routing.ErrTooManyExclusions):
		return "Слишком много перекрытых участков для службы маршрутизации: задайте перекрытие областью"
	case errors.Is(err, routing.ErrUnavailable):
		return "Служба маршрутизации недоступна"
	case errors.Is(err, routing.ErrElementNotFound):
		return "Элемента сети нет в последнем анализе: выберите перекрытие заново"
	case errors.Is(err, routing.ErrNoLocation):
		return "Точка сценария не привязывается к дорожной сети"
	case errors.Is(err, context.DeadlineExceeded):
		return "Расчёт не уложился в отведённое время"
	}
	return "Ошибка расчёта: " + err.Error()
}

// run считает сценарий; итог или ошибка записываются в сценарий, о завершении сообщает событие
// scenario.computed.
func (s *Service) run(ctx context.Context, id uuid.UUID) error {
	var sc *Scenario
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		sc, err = scanScenario(tx.QueryRow(ctx, `UPDATE scenario.scenario
			SET status = 'running', started_at = now(), progress = '{}', error = NULL
			WHERE id = $1 RETURNING `+scenarioColumns, id))
		return err
	})
	if err != nil {
		return err
	}
	started := time.Now()
	summary, built, err := s.compute(ctx, sc)
	status, msg := StatusDone, (*string)(nil)
	if err != nil {
		status = StatusFailed
		m := UserMessage(err)
		msg = &m
	} else {
		summary.DurationMs = int(time.Since(started).Milliseconds())
	}
	// Итог записывается и при отменённом контексте задачи (остановка процесса).
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var sum []byte
	if summary != nil && err == nil {
		sum, _ = json.Marshal(summary)
	}
	werr := db.InTx(wctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(wctx, `UPDATE scenario.scenario
			SET status = $2, summary = $3, error = $4, graph_built_at = $5, finished_at = now()
			WHERE id = $1`, id, status, sum, msg, built); err != nil {
			return err
		}
		return events.Publish(wctx, tx, events.ScenarioComputed, map[string]any{
			"userId": sc.UserID, "id": id, "status": status,
		})
	})
	return errors.Join(err, werr)
}

func (s *Service) compute(ctx context.Context, sc *Scenario) (*Summary, *time.Time, error) {
	p := sc.Params
	tr := &tracker{s: s, id: sc.ID, userID: sc.UserID}
	tr.stage(ctx, "Подготовка")
	built, err := s.routing.GraphBuiltAt(ctx)
	if err != nil {
		return nil, nil, err
	}
	prof, err := s.routing.ProfileByCode(ctx, p.Profile)
	if err != nil {
		return nil, &built, invalid("неизвестный профиль ТС %q", p.Profile)
	}
	depart := time.Now()
	if p.Depart != nil {
		depart = *p.Depart
	}

	ents, err := s.entities(ctx, p)
	if err != nil {
		return nil, &built, err
	}
	points := make([]routing.Point, len(ents))
	for i, e := range ents {
		points[i] = e.point
	}
	base, scen := &variant{}, &variant{}
	summary := &Summary{Facilities: len(ents)}
	if p.NewFacility != nil {
		summary.Facilities--
	}
	if p.Zones {
		if err := s.zoneVariants(ctx, p, prof, depart, points, base, scen, summary); err != nil {
			return nil, &built, err
		}
	}
	closures, err := s.closures(ctx, p, scen)
	if err != nil {
		return nil, &built, err
	}
	summary.Closures = closures

	targets, err := s.routing.Targets(ctx)
	if err != nil {
		return nil, &built, err
	}
	// База без правил зон на том же графе — показатели доступности (тот же расчёт матрицами).
	reuse := !p.Zones
	type kindResult struct{ base, scen []float64 }
	results := make(map[string]kindResult, len(p.Targets))

	for _, k := range p.Targets {
		tr.p.Total += 2 * batchCount(len(ents), len(targets[k]))
	}
	tr.p.Total += 2 * len(isoOrigins(p))

	for _, kind := range p.Targets {
		ts := targets[kind]
		var r kindResult
		var reused bool
		if reuse {
			r.base, reused, err = s.reuseBase(ctx, ents, prof.Code, kind, built)
			if err != nil {
				return nil, &built, err
			}
		}
		tr.stage(ctx, "Доступность "+targetTitle(kind)+": база")
		if reused {
			summary.BaseReused = true
			tr.add(ctx, batchCount(len(ents), len(ts)))
			// Новый объект в показателях доступности не участвует — считается отдельно.
			if p.NewFacility != nil {
				last := len(ents) - 1
				v, err := s.nearest(ctx, tr, ents[last:], ts, prof, base, false)
				if err != nil {
					return nil, &built, err
				}
				r.base[last] = v[0]
			}
		} else if r.base, err = s.nearest(ctx, tr, ents, ts, prof, base, true); err != nil {
			return nil, &built, err
		}
		tr.stage(ctx, "Доступность "+targetTitle(kind)+": сценарий")
		if r.scen, err = s.nearest(ctx, tr, ents, ts, prof, scen, true); err != nil {
			return nil, &built, err
		}
		results[kind] = r
	}

	tr.stage(ctx, "Изохроны")
	isos, err := s.isochrones(ctx, tr, sc, prof, depart, base, scen, points, ents)
	if err != nil {
		return nil, &built, err
	}
	summary.Isochrones = isos

	tr.stage(ctx, "Сохранение")
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM scenario.facility_delta WHERE scenario_id = $1`, sc.ID); err != nil {
			return err
		}
		var rows [][]any
		for _, kind := range p.Targets {
			r := results[kind]
			for i, e := range ents {
				bm, bs := cell(r.base[i], base.blocked[i])
				sm, ss := cell(r.scen[i], scen.blocked[i])
				rows = append(rows, []any{sc.ID, e.key, kind, bm, sm, bs, ss})
			}
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"scenario", "facility_delta"},
			[]string{"scenario_id", "entity", "target_kind", "base_min", "scenario_min", "base_status", "scenario_status"},
			pgx.CopyFromRows(rows))
		return err
	})
	if err != nil {
		return nil, &built, err
	}
	for _, kind := range p.Targets {
		r := results[kind]
		summary.Targets = append(summary.Targets, summarize(kind, r.base, r.scen, base.blocked, scen.blocked, p.NewFacility != nil))
		if p.NewFacility != nil {
			last := len(ents) - 1
			bm, bs := cell(r.base[last], base.blocked[last])
			sm, ss := cell(r.scen[last], scen.blocked[last])
			summary.NewFacility = append(summary.NewFacility, EntityTime{
				Kind: kind, BaseMin: bm, ScenarioMin: sm, BaseStatus: bs, ScenarioStatus: ss,
			})
		}
	}
	return summary, &built, nil
}

func targetTitle(kind string) string {
	switch kind {
	case "mkad":
		return "до МКАД"
	case "ttk":
		return "до ТТК"
	case "center":
		return "до центра"
	case "rail_freight":
		return "до ж/д грузовых пунктов"
	}
	return kind
}

// cell — значение и статус ячейки результата.
func cell(minutes float64, blocked bool) (*float64, string) {
	switch {
	case blocked:
		return nil, "blocked"
	case minutes < 0:
		return nil, "unreachable"
	}
	v := math.Round(minutes*100) / 100
	return &v, "ok"
}

// entities — действующие объекты реестра (как в показателях доступности) и новый объект последним.
func (s *Service) entities(ctx context.Context, p Params) ([]entity, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, ST_X(point), ST_Y(point) FROM infra.facility WHERE status = 'active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	ents, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (entity, error) {
		var e entity
		return e, r.Scan(&e.key, &e.point.Lon, &e.point.Lat)
	})
	if err != nil {
		return nil, err
	}
	if f := p.NewFacility; f != nil {
		ents = append(ents, entity{key: newEntity, point: routing.Point{Lon: f.Lon, Lat: f.Lat}})
	}
	return ents, nil
}

// zoneVariants — решения по зонам базы и сценария (переопределения), кольца закрытых зон и точки
// внутри закрытых зон.
func (s *Service) zoneVariants(ctx context.Context, p Params, prof routing.Profile, depart time.Time,
	points []routing.Point, base, scen *variant, summary *Summary,
) error {
	rules, err := s.routing.Rules(ctx)
	if err != nil {
		return err
	}
	passes := make([]routing.Pass, len(p.Passes))
	for i, ps := range p.Passes {
		passes[i] = routing.Pass{Zone: ps.Zone, Kind: ps.Kind}
	}
	vehicle := routing.Vehicle{GrossT: prof.GrossT, PayloadT: prof.PayloadT, EcoClass: prof.EcoClass, Hazmat: prof.Hazmat}
	base.zones = routing.Decide(rules, vehicle, passes, depart)
	scen.zones = applyOverrides(base.zones, p.ZoneOverrides)
	for i, d := range base.zones {
		summary.Zones = append(summary.Zones, ZoneChange{
			Zone: d.Zone, BaseClosed: d.Forbidden, ScenarioClosed: scen.zones[i].Forbidden,
		})
	}
	for _, v := range []*variant{base, scen} {
		v.blocked = map[int]bool{}
		closed := routing.ClosedZone(v.zones)
		if closed == "" {
			continue
		}
		rings, err := s.routing.ZoneRing(ctx, closed)
		if err != nil {
			return err
		}
		v.ex.Polygons = append(v.ex.Polygons, rings...)
		bl, err := s.routing.BlockedPoints(ctx, points, v.zones)
		if err != nil {
			return err
		}
		for _, b := range bl {
			v.blocked[b.Index] = true
		}
	}
	return nil
}

// applyOverrides — решения по зонам с переопределениями сценария. Открытая внешняя зона не
// открывает внутренние: каждая зона — своё решение.
func applyOverrides(ds []routing.ZoneDecision, over map[string]bool) []routing.ZoneDecision {
	out := slices.Clone(ds)
	for i := range out {
		if open, ok := over[out[i].Zone]; ok {
			out[i].Forbidden = !open
			out[i].Rules = nil
		}
	}
	return out
}

// closures переводит перекрытия в исключения сценария и описывает их для итога.
func (s *Service) closures(ctx context.Context, p Params, scen *variant) ([]ClosureInfo, error) {
	out := make([]ClosureInfo, 0, len(p.Closures))
	for _, c := range p.Closures {
		info := ClosureInfo{Kind: c.Kind, Label: c.Label}
		var segments []uuid.UUID
		switch c.Kind {
		case ClosureSegments:
			segments = c.Segments
		case ClosureElement:
			el, err := s.routing.Element(ctx, *c.Element)
			if err != nil {
				return nil, err
			}
			segments = el.Segments
			if info.Label == "" {
				info.Label = el.Name
			}
		case ClosureArea:
			ring := make(routing.Ring, len(c.Polygon))
			copy(ring, c.Polygon)
			scen.ex.Polygons = append(scen.ex.Polygons, ring)
			if err := s.pool.QueryRow(ctx, `
				WITH g AS (SELECT ST_MakeValid(ST_MakePolygon(ST_GeomFromGeoJSON($1))) AS g)
				SELECT ST_Area(g::geography) / 1e6, count(sg.id)::int, coalesce(sum(sg.length_m), 0) / 1000
				FROM g LEFT JOIN network.road_segment sg ON sg.geom && g.g AND ST_Intersects(sg.geom, g.g)
				GROUP BY g.g`, lineGeoJSON(c.Polygon)).Scan(&info.AreaKm2, &info.Segments, &info.LengthKm); err != nil {
				return nil, fmt.Errorf("closure area: %w", err)
			}
		}
		if len(segments) > 0 {
			pts, err := s.routing.SegmentExclusions(ctx, p.Profile, segments)
			if err != nil {
				return nil, err
			}
			scen.ex.Locations = append(scen.ex.Locations, pts...)
			info.Excluded = len(pts)
			var names []string
			if err := s.pool.QueryRow(ctx, `
				SELECT count(*)::int, coalesce(sum(length_m), 0) / 1000,
				       coalesce(array_agg(DISTINCT name) FILTER (WHERE name IS NOT NULL), '{}')
				FROM network.road_segment WHERE id = ANY ($1)`, segments).Scan(&info.Segments, &info.LengthKm, &names); err != nil {
				return nil, err
			}
			if info.Label == "" && len(names) > 0 {
				slices.Sort(names)
				info.Label = names[0]
				if len(names) > 1 {
					info.Label += fmt.Sprintf(" и ещё %d", len(names)-1)
				}
			}
		}
		info.LengthKm = math.Round(info.LengthKm*100) / 100
		info.AreaKm2 = math.Round(info.AreaKm2*100) / 100
		out = append(out, info)
	}
	if len(scen.ex.Locations) > routing.MaxExcludeLocations {
		return nil, routing.ErrTooManyExclusions
	}
	return out, nil
}

// lineGeoJSON — контур области как LineString GeoJSON (для ST_MakePolygon).
func lineGeoJSON(ring [][2]float64) string {
	b, _ := json.Marshal(map[string]any{"type": "LineString", "coordinates": ring})
	return string(b)
}

// reuseBase — время до ближайшей цели из показателей доступности, если они посчитаны на текущем
// графе для всех объектов реестра. Новый объект (последний) — -1, его считают отдельно.
func (s *Service) reuseBase(ctx context.Context, ents []entity, profile, kind string, built time.Time) ([]float64, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT facility_id::text, coalesce(minutes, -1)::float8 FROM analytics.accessibility
		WHERE profile_code = $1 AND target_kind = $2 AND graph_built_at = $3`, profile, kind, built)
	if err != nil {
		return nil, false, err
	}
	have := map[string]float64{}
	for rows.Next() {
		var id string
		var m float64
		if err := rows.Scan(&id, &m); err != nil {
			rows.Close()
			return nil, false, err
		}
		have[id] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	out := make([]float64, len(ents))
	for i, e := range ents {
		if e.key == newEntity {
			out[i] = -1
			continue
		}
		m, ok := have[e.key]
		if !ok {
			return nil, false, nil
		}
		out[i] = m
	}
	return out, true, nil
}

// nearest — время до ближайшей цели для объектов вне закрытых зон варианта (закрытые — -1).
func (s *Service) nearest(ctx context.Context, tr *tracker, ents []entity, targets []routing.Point, prof routing.Profile,
	v *variant, track bool,
) ([]float64, error) {
	out := make([]float64, len(ents))
	var idx []int
	var src []routing.Point
	for i, e := range ents {
		out[i] = -1
		if !v.blocked[i] {
			idx = append(idx, i)
			src = append(src, e.point)
		}
	}
	if len(src) == 0 {
		return out, nil
	}
	in := routing.NearestInput{Sources: src, Targets: targets, Profile: prof, Exclude: v.ex, Workers: s.workers}
	if track {
		// Отчёты приходят из параллельных запросов; пропущенные (закрытые) объекты сокращают
		// число запросов — ход догоняется в конце.
		var mu sync.Mutex
		var counted int
		in.Progress = func(int, int) {
			mu.Lock()
			counted++
			mu.Unlock()
			tr.add(ctx, 1)
		}
		defer func() {
			if rest := batchCount(len(ents), len(targets)) - counted; rest > 0 {
				tr.add(ctx, rest)
			}
		}()
	}
	minutes, _, err := s.routing.Nearest(ctx, in)
	if err != nil {
		return nil, err
	}
	for j, i := range idx {
		out[i] = minutes[j]
	}
	return out, nil
}

// batchCount — запросов матрицы на n источников и targets целей (порции как в routing.Nearest:
// до 50 источников и 2400 пар).
func batchCount(n, targets int) int {
	if n == 0 || targets == 0 {
		return 0
	}
	b := min(50, max(1, 2400/targets))
	return (n + b - 1) / b
}

// isoOrigins — источники изохрон: новый объект и выбранные объекты реестра.
func isoOrigins(p Params) []string {
	if len(p.IsochroneMinutes) == 0 {
		return nil
	}
	var out []string
	if p.NewFacility != nil {
		out = append(out, newEntity)
	}
	for _, id := range p.IsochroneFacilities {
		out = append(out, id.String())
	}
	return out
}

// isochrones строит и сохраняет изохроны базы и сценария.
func (s *Service) isochrones(ctx context.Context, tr *tracker, sc *Scenario, prof routing.Profile, depart time.Time,
	base, scen *variant, points []routing.Point, ents []entity,
) ([]IsoSummary, error) {
	p := sc.Params
	origins := isoOrigins(p)
	if _, err := s.pool.Exec(ctx, `DELETE FROM scenario.isochrone WHERE scenario_id = $1`, sc.ID); err != nil {
		return nil, err
	}
	if len(origins) == 0 {
		return nil, nil
	}
	index := make(map[string]int, len(ents))
	for i, e := range ents {
		index[e.key] = i
	}
	var dep time.Time
	if p.Depart != nil {
		dep = depart
	}
	for _, o := range origins {
		pt, i, ok := routing.Point{}, 0, false
		if i, ok = index[o]; ok {
			pt = points[i]
		} else {
			// Объект не действует (или исключён из реестра) — берём его точку.
			if err := s.pool.QueryRow(ctx, `SELECT ST_X(point), ST_Y(point) FROM infra.facility WHERE id = $1`, o).
				Scan(&pt.Lon, &pt.Lat); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					tr.add(ctx, 2)
					continue
				}
				return nil, err
			}
		}
		for _, v := range []struct {
			name string
			v    *variant
		}{{"base", base}, {"scenario", scen}} {
			tr.add(ctx, 1)
			if ok && v.v.blocked[i] {
				continue
			}
			gj, err := s.routing.IsochroneWith(ctx, pt, prof, p.IsochroneMinutes, dep, v.v.ex)
			if errors.Is(err, routing.ErrNoLocation) || errors.Is(err, routing.ErrNoRoute) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("isochrone %s: %w", o, err)
			}
			if _, err := s.pool.Exec(ctx, `
				INSERT INTO scenario.isochrone (scenario_id, origin, minutes, variant, geom, area_km2)
				SELECT $1, $2, (f->'properties'->>'contour')::numeric::int, $3, g, ST_Area(g::geography) / 1e6
				FROM jsonb_array_elements($4::jsonb->'features') f,
				     LATERAL (SELECT ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_GeomFromGeoJSON(f->'geometry')), 3)) AS g) x
				WHERE f->'properties' ? 'contour' AND NOT ST_IsEmpty(g)
				ON CONFLICT DO NOTHING`, sc.ID, o, v.name, string(gj)); err != nil {
				return nil, fmt.Errorf("store isochrone: %w", err)
			}
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.origin, coalesce(f.name, $2, ''), i.minutes,
		       coalesce(sum(i.area_km2) FILTER (WHERE i.variant = 'base'), 0),
		       coalesce(sum(i.area_km2) FILTER (WHERE i.variant = 'scenario'), 0)
		FROM scenario.isochrone i
		LEFT JOIN infra.facility f ON f.id = nullif(i.origin, 'new')::uuid
		WHERE i.scenario_id = $1
		GROUP BY i.origin, f.name, i.minutes ORDER BY i.origin, i.minutes`, sc.ID, newName(p))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (IsoSummary, error) {
		var x IsoSummary
		err := r.Scan(&x.Origin, &x.Name, &x.Minutes, &x.BaseKm2, &x.ScenarioKm2)
		x.BaseKm2 = math.Round(x.BaseKm2*100) / 100
		x.ScenarioKm2 = math.Round(x.ScenarioKm2*100) / 100
		return x, err
	})
}

func newName(p Params) *string {
	if p.NewFacility == nil {
		return nil
	}
	return &p.NewFacility.Name
}

// eps — разность времени, которую считаем изменением, мин (округление службы — секунды).
const eps = 0.05

// summarize — изменения доступности одного вида целей по объектам реестра (без нового объекта).
func summarize(kind string, base, scen []float64, baseBlocked, scenBlocked map[int]bool, hasNew bool) TargetSummary {
	t := TargetSummary{Kind: kind}
	n := len(base)
	if hasNew {
		n--
	}
	var bOK, sOK []float64
	for i := range n {
		bok := !baseBlocked[i] && base[i] >= 0
		sok := !scenBlocked[i] && scen[i] >= 0
		if scenBlocked[i] {
			t.Blocked++
		}
		if bok {
			bOK = append(bOK, base[i])
		}
		if sok {
			sOK = append(sOK, scen[i])
		}
		switch {
		case bok && !sok:
			t.Lost++
		case !bok && sok:
			t.Gained++
		case bok && sok && scen[i]-base[i] > eps:
			d := scen[i] - base[i]
			t.Affected++
			t.DelayMin += d
			t.MaxDelay = max(t.MaxDelay, d)
		case bok && sok && base[i]-scen[i] > eps:
			t.Improved++
		}
	}
	if t.Affected > 0 {
		t.MeanDelay = t.DelayMin / float64(t.Affected)
	}
	t.BaseMedian, t.ScenarioMedian = median(bOK), median(sOK)
	round := func(v float64) float64 { return math.Round(v*100) / 100 }
	t.DelayMin, t.MeanDelay, t.MaxDelay = round(t.DelayMin), round(t.MeanDelay), round(t.MaxDelay)
	t.BaseMedian, t.ScenarioMedian = round(t.BaseMedian), round(t.ScenarioMedian)
	return t
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

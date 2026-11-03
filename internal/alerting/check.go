package alerting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/ids"
)

const (
	// lookback — за сколько дней ищутся изменения: проверка, пропущенная из-за простоя,
	// догоняет их; повторов нет — срабатывания записаны в alerting.hit.
	lookback = 14 * 24 * time.Hour
	// hitsPerCheck — срабатываний правила за одну проверку; остальные — в следующей.
	hitsPerCheck = 500
	// maxTracked — ограничений в области правила: больше — область нужно уменьшить.
	maxTracked = 20000
)

// rule — правило в проверке.
type rule struct {
	ID        uuid.UUID
	OwnerID   uuid.UUID
	Kind      string
	Scope     Scope
	Params    Params
	Severity  string
	State     json.RawMessage
	CreatedAt time.Time
}

// hit — срабатывание: ключ изменения (для дедупликации) и что сообщить.
type hit struct {
	Key      string
	Entity   string
	Severity string
	Title    string
	Summary  string
	Details  any
	Geometry string // GeoJSON или пусто
}

// CheckAll проверяет все включённые правила; ошибка одного записывается в правило и не
// останавливает остальные. Возвращает число новых срабатываний.
func (s *Service) CheckAll(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM alerting.rule WHERE enabled ORDER BY created_at`)
	if err != nil {
		return 0, fmt.Errorf("list rules: %w", err)
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range list {
		n, err := s.CheckRule(ctx, id)
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		if err != nil {
			s.log.WarnContext(ctx, "alert rule check failed", "rule", id, "err", err)
			msg := err.Error()
			var inv *ErrInvalid
			if errors.As(err, &inv) {
				msg = inv.Reason
			}
			if _, uerr := s.pool.Exec(ctx, `UPDATE alerting.rule SET check_error = $2, checked_at = now() WHERE id = $1`, id, msg); uerr != nil {
				return total, uerr
			}
			continue
		}
		total += n
	}
	return total, nil
}

// CheckRule проверяет одно правило в транзакции: срабатывания, алерты, события и новое
// состояние фиксируются вместе. Одновременные проверки правила идут по очереди.
func (s *Service) CheckRule(ctx context.Context, id uuid.UUID) (int, error) {
	n := 0
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var r rule
		var scope, params []byte
		err := tx.QueryRow(ctx, `
			SELECT id, owner_id, kind, scope, params, severity, state, created_at
			FROM alerting.rule WHERE id = $1 AND enabled FOR UPDATE`, id).
			Scan(&r.ID, &r.OwnerID, &r.Kind, &scope, &params, &r.Severity, &r.State, &r.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // удалено или выключено
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(scope, &r.Scope); err != nil {
			return err
		}
		if err := json.Unmarshal(params, &r.Params); err != nil {
			return err
		}
		var hits []hit
		var state any
		var resolve []string
		switch r.Kind {
		case KindRoadworks:
			hits, resolve, err = roadworkHits(ctx, tx, &r)
		case KindRestrictions:
			hits, state, err = restrictionHits(ctx, tx, &r)
		case KindAccessibility:
			hits, state, err = accessibilityHits(ctx, tx, &r)
		case KindFacilities:
			hits, err = facilityHits(ctx, tx, &r)
		case KindSources:
			hits, resolve, err = sourceHits(ctx, tx, &r)
		default:
			err = fmt.Errorf("unknown rule kind %q", r.Kind)
		}
		if err != nil {
			return err
		}
		for _, h := range hits {
			ok, err := s.raise(ctx, tx, &r, h)
			if err != nil {
				return err
			}
			if ok {
				n++
			}
		}
		for _, entity := range resolve {
			if err := s.resolve(ctx, tx, &r, entity); err != nil {
				return err
			}
		}
		var stateJSON []byte
		if state != nil {
			if stateJSON, err = json.Marshal(state); err != nil {
				return err
			}
		} else {
			stateJSON = r.State
		}
		_, err = tx.Exec(ctx, `UPDATE alerting.rule SET state = $2, checked_at = now(), check_error = NULL WHERE id = $1`, r.ID, stateJSON)
		return err
	})
	return n, err
}

// raise записывает срабатывание и создаёт алерт или добавляет повтор к открытому алерту по
// той же сущности. Уже записанное срабатывание пропускается.
func (s *Service) raise(ctx context.Context, tx pgx.Tx, r *rule, h hit) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO alerting.hit (rule_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, r.ID, h.Key)
	if err != nil {
		return false, fmt.Errorf("record hit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	details, err := json.Marshal(h.Details)
	if err != nil {
		return false, err
	}
	var alertID uuid.UUID
	var severity string
	typ := events.AlertUpdated
	action := "repeated"
	err = tx.QueryRow(ctx, `
		UPDATE alerting.alert SET occurrences = occurrences + 1, last_seen_at = now(), updated_at = now(),
			severity = CASE WHEN array_position(ARRAY['info','warning','critical'], $3) >
				array_position(ARRAY['info','warning','critical'], severity) THEN $3 ELSE severity END,
			title = $4, summary = $5, details = $6, geom = coalesce(ST_GeomFromGeoJSON(NULLIF($7, '')), geom)
		WHERE rule_id = $1 AND entity = $2 AND status <> 'closed'
		RETURNING id, severity`, r.ID, h.Entity, h.Severity, h.Title, h.Summary, details, h.Geometry).Scan(&alertID, &severity)
	if errors.Is(err, pgx.ErrNoRows) {
		typ, action, alertID, severity = events.AlertCreated, "created", ids.New(), h.Severity
		_, err = tx.Exec(ctx, `
			INSERT INTO alerting.alert (id, rule_id, entity, severity, title, summary, details, geom)
			VALUES ($1, $2, $3, $4, $5, $6, $7, ST_GeomFromGeoJSON(NULLIF($8, '')))`,
			alertID, r.ID, h.Entity, h.Severity, h.Title, h.Summary, details, h.Geometry)
	}
	if err != nil {
		return false, fmt.Errorf("raise alert: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE alerting.hit SET alert_id = $3 WHERE rule_id = $1 AND key = $2`, r.ID, h.Key, alertID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO alerting.alert_log (alert_id, action, details) VALUES ($1, $2, $3)`,
		alertID, action, details); err != nil {
		return false, err
	}
	return true, events.Publish(ctx, tx, typ, alertEvent(alertID, r, severity, action))
}

// resolve закрывает открытый алерт сущности, если причина исчезла (работа завершена, источник
// снова загружается).
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, r *rule, entity string) error {
	var alertID uuid.UUID
	var severity string
	err := tx.QueryRow(ctx, `
		UPDATE alerting.alert SET status = 'closed', closed_at = now(), resolution = 'auto', updated_at = now()
		WHERE rule_id = $1 AND entity = $2 AND status <> 'closed' RETURNING id, severity`, r.ID, entity).Scan(&alertID, &severity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO alerting.alert_log (alert_id, action) VALUES ($1, 'resolved')`, alertID); err != nil {
		return err
	}
	return events.Publish(ctx, tx, events.AlertUpdated, alertEvent(alertID, r, severity, "resolved"))
}

func alertEvent(id uuid.UUID, r *rule, severity, action string) map[string]any {
	return map[string]any{"alertId": id, "ruleId": r.ID, "ownerId": r.OwnerID, "kind": r.Kind, "severity": severity, "action": action}
}

var changeTitles = map[string]string{"appeared": "новая", "changed": "изменена", "started": "началась"}

var workTitles = map[string]string{"repair": "Дорожная работа", "emergency": "Аварийная работа", "closure": "Перекрытие"}

// roadworkHits — изменения работ в области правила; завершённые и снятые работы закрывают свои
// алерты.
func roadworkHits(ctx context.Context, tx pgx.Tx, r *rule) ([]hit, []string, error) {
	since := r.CreatedAt
	if floor := time.Now().Add(-lookback); since.Before(floor) {
		since = floor
	}
	rows, err := tx.Query(ctx, `
		SELECT e.id, e.type, w.id, w.kind, w.title, coalesce(w.place, ''), coalesce(w.district, ''), w.closure,
			coalesce(w.lanes_closed, ''), w.planned_start, w.planned_end, coalesce(ST_AsGeoJSON(w.geom, 6), '')
		FROM operations.road_work_event e
		JOIN operations.road_work w ON w.id = e.work_id
		JOIN alerting.rule ru ON ru.id = $1
		WHERE e.at >= $2 AND e.type = ANY($3)
		  AND (cardinality(coalesce($4::text[], '{}')) = 0 OR w.kind = ANY($4))
		  AND (NOT $5 OR w.closure)
		  AND w.geom && ru.area AND ST_Intersects(w.geom, ru.area)
		  AND NOT EXISTS (SELECT 1 FROM alerting.hit h WHERE h.rule_id = $1 AND h.key = 'rwe:' || e.id)
		ORDER BY e.id LIMIT $6`,
		r.ID, since, r.Params.Changes, r.Params.WorkKinds, r.Params.ClosureOnly, hitsPerCheck)
	if err != nil {
		return nil, nil, fmt.Errorf("roadwork changes: %w", err)
	}
	defer rows.Close()
	var hits []hit
	for rows.Next() {
		var eventID int64
		var change, kind, title, place, district, lanes, geom string
		var workID uuid.UUID
		var closure bool
		var start, end *time.Time
		if err := rows.Scan(&eventID, &change, &workID, &kind, &title, &place, &district, &closure, &lanes,
			&start, &end, &geom); err != nil {
			return nil, nil, err
		}
		sev := r.Severity
		if kind == "emergency" {
			sev = SeverityCritical
		} else if closure {
			sev = maxSeverity(sev, SeverityWarning)
		}
		parts := []string{}
		if place != "" {
			parts = append(parts, place)
		}
		if p := period(start, end); p != "" {
			parts = append(parts, p)
		}
		if closure {
			parts = append(parts, "с перекрытием движения")
		}
		hits = append(hits, hit{
			Key: fmt.Sprintf("rwe:%d", eventID), Entity: "road_work:" + workID.String(), Severity: sev,
			Title:   fmt.Sprintf("%s (%s): %s", workTitles[kind], changeTitles[change], title),
			Summary: strings.Join(parts, "; "),
			Details: map[string]any{
				"workId": workID, "workKind": kind, "change": change, "place": place,
				"district": district, "closure": closure, "lanesClosed": lanes, "plannedStart": start, "plannedEnd": end,
			},
			Geometry: geom,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	resolve, err := entities(ctx, tx, `
		SELECT a.entity FROM alerting.alert a
		JOIN operations.road_work w ON a.entity = 'road_work:' || w.id
		WHERE a.rule_id = $1 AND a.status <> 'closed' AND w.state IN ('finished', 'removed')`, r.ID)
	return hits, resolve, err
}

func period(start, end *time.Time) string {
	f := func(t *time.Time) string { return t.Format("02.01.2006") }
	switch {
	case start != nil && end != nil:
		return f(start) + "–" + f(end)
	case start != nil:
		return "с " + f(start)
	case end != nil:
		return "до " + f(end)
	}
	return ""
}

func entities(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// tracked — запомненное значение ограничения или доступности.
type tracked struct {
	Value string  `json:"v"`
	Name  string  `json:"n,omitempty"`
	Lon   float64 `json:"x"`
	Lat   float64 `json:"y"`
}

var restrictionTitles = map[string]string{
	"hgv_ban": "запрет грузовиков", "motor_vehicle_ban": "запрет движения", "hgv_destination": "грузовикам только к месту назначения",
	"maxheight": "ограничение высоты", "maxweight": "ограничение массы", "maxaxleload": "ограничение нагрузки на ось",
	"maxwidth": "ограничение ширины", "maxlength": "ограничение длины", "hazmat_ban": "запрет опасных грузов",
}

// restrictionHits сравнивает ограничения в области с прошлой проверкой. Ключ ограничения —
// вид, направление и путь OSM: перезагрузка сети меняет номера участков, но не пути. Первая
// проверка только запоминает состояние. Изменения одной проверки собираются в один алерт.
func restrictionHits(ctx context.Context, tx pgx.Tx, r *rule) ([]hit, any, error) {
	rows, err := tx.Query(ctx, `
		SELECT rs.kind || '|' || coalesce(rs.direction::text, '') || '|' || s.osm_id,
			string_agg(DISTINCT coalesce(rs.value::text, '+'), ',' ORDER BY coalesce(rs.value::text, '+')),
			coalesce(min(s.name), ''), ST_X(ST_PointOnSurface(ST_Collect(s.geom))), ST_Y(ST_PointOnSurface(ST_Collect(s.geom)))
		FROM network.restriction rs
		JOIN network.road_segment s ON s.id = rs.segment_id
		JOIN alerting.rule ru ON ru.id = $1
		WHERE s.geom && ru.area AND ST_Intersects(s.geom, ru.area)
		  AND (cardinality(coalesce($2::text[], '{}')) = 0 OR rs.kind = ANY($2))
		GROUP BY 1`, r.ID, r.Params.RestrictionKinds)
	if err != nil {
		return nil, nil, fmt.Errorf("restrictions in area: %w", err)
	}
	now := map[string]tracked{}
	for rows.Next() {
		var key string
		var t tracked
		if err := rows.Scan(&key, &t.Value, &t.Name, &t.Lon, &t.Lat); err != nil {
			rows.Close()
			return nil, nil, err
		}
		now[key] = t
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(now) > maxTracked {
		return nil, nil, invalid("в области больше %d ограничений — уменьшите область", maxTracked)
	}
	if r.State == nil {
		return nil, now, nil
	}
	var before map[string]tracked
	if err := json.Unmarshal(r.State, &before); err != nil {
		return nil, nil, err
	}
	type change struct {
		Op    string  `json:"op"` // added, removed, changed
		Kind  string  `json:"kind"`
		Title string  `json:"title"`
		Name  string  `json:"name"`
		From  string  `json:"from,omitempty"`
		To    string  `json:"to,omitempty"`
		Lon   float64 `json:"lon"`
		Lat   float64 `json:"lat"`
	}
	var changes []change
	counts := map[string]int{}
	for _, key := range slices.Sorted(maps.Keys(now)) {
		t := now[key]
		kind := strings.SplitN(key, "|", 2)[0]
		old, ok := before[key]
		switch {
		case !ok:
			changes = append(changes, change{Op: "added", Kind: kind, Title: restrictionTitles[kind], Name: t.Name, To: t.Value, Lon: t.Lon, Lat: t.Lat})
		case old.Value != t.Value:
			changes = append(changes, change{Op: "changed", Kind: kind, Title: restrictionTitles[kind], Name: t.Name, From: old.Value, To: t.Value, Lon: t.Lon, Lat: t.Lat})
		default:
			continue
		}
		counts[changes[len(changes)-1].Op]++
	}
	for _, key := range slices.Sorted(maps.Keys(before)) {
		if _, ok := now[key]; ok {
			continue
		}
		t := before[key]
		kind := strings.SplitN(key, "|", 2)[0]
		changes = append(changes, change{Op: "removed", Kind: kind, Title: restrictionTitles[kind], Name: t.Name, From: t.Value, Lon: t.Lon, Lat: t.Lat})
		counts["removed"]++
	}
	if len(changes) == 0 {
		return nil, now, nil
	}
	sum := sha256.New()
	pts := make([]string, 0, len(changes))
	for _, c := range changes {
		fmt.Fprintf(sum, "%s|%s|%s|%s|%s;", c.Op, c.Kind, c.Name, c.From, c.To)
		pts = append(pts, fmt.Sprintf("[%.6f,%.6f]", c.Lon, c.Lat))
	}
	shown := changes
	if len(shown) > 100 {
		shown = shown[:100]
	}
	parts := []string{}
	for _, op := range []struct{ code, title string }{{"added", "новых"}, {"changed", "изменённых"}, {"removed", "снятых"}} {
		if counts[op.code] > 0 {
			parts = append(parts, fmt.Sprintf("%s — %d", op.title, counts[op.code]))
		}
	}
	h := hit{
		Key:      "rst:" + hex.EncodeToString(sum.Sum(nil))[:24] + ":" + time.Now().UTC().Format(time.RFC3339),
		Entity:   "restrictions",
		Severity: r.Severity,
		Title:    fmt.Sprintf("Изменились ограничения в области: %d", len(changes)),
		Summary:  strings.Join(parts, ", "),
		Details:  map[string]any{"changes": shown, "total": len(changes), "counts": counts},
		Geometry: `{"type":"MultiPoint","coordinates":[` + strings.Join(pts, ",") + `]}`,
	}
	return []hit{h}, now, nil
}

var targetTitles = map[string]string{"mkad": "МКАД", "ttk": "ТТК", "center": "центра", "rail_freight": "ж/д пункта"}

// accessibilityHits сравнивает время до цели у объектов правила с прошлой проверкой: прирост не
// меньше порога или потеря доступности — алерт по объекту. Первая проверка только запоминает.
func accessibilityHits(ctx context.Context, tx pgx.Tx, r *rule) ([]hit, any, error) {
	rows, err := tx.Query(ctx, `
		SELECT f.id::text, coalesce(f.name, ''), coalesce(a.minutes::text, ''), ST_X(f.point), ST_Y(f.point)
		FROM infra.facility f
		LEFT JOIN analytics.accessibility a ON a.facility_id = f.id AND a.profile_code = $2 AND a.target_kind = $3
		WHERE f.id = ANY($1)`, r.Scope.FacilityIDs, r.Params.Profile, r.Params.Target)
	if err != nil {
		return nil, nil, fmt.Errorf("facility accessibility: %w", err)
	}
	now := map[string]tracked{}
	for rows.Next() {
		var id string
		var t tracked
		if err := rows.Scan(&id, &t.Name, &t.Value, &t.Lon, &t.Lat); err != nil {
			rows.Close()
			return nil, nil, err
		}
		now[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if r.State == nil {
		return nil, now, nil
	}
	var before map[string]tracked
	if err := json.Unmarshal(r.State, &before); err != nil {
		return nil, nil, err
	}
	var hits []hit
	target := targetTitles[r.Params.Target]
	for _, id := range slices.Sorted(maps.Keys(now)) {
		t, old := now[id], before[id]
		if old.Value == "" || old.Value == t.Value {
			continue // раньше недоступен (или не посчитан) — ухудшения нет
		}
		var oldMin, newMin float64
		if _, err := fmt.Sscan(old.Value, &oldMin); err != nil {
			continue
		}
		h := hit{
			Entity:   "facility:" + id,
			Geometry: fmt.Sprintf(`{"type":"Point","coordinates":[%.6f,%.6f]}`, t.Lon, t.Lat),
		}
		details := map[string]any{"facilityId": id, "facility": t.Name, "profile": r.Params.Profile, "target": r.Params.Target, "before": oldMin}
		if t.Value == "" {
			h.Severity = maxSeverity(r.Severity, SeverityWarning)
			h.Title = fmt.Sprintf("%s: недоступен от %s", t.Name, target)
			h.Summary = fmt.Sprintf("Было %s мин, маршрута для профиля %s больше нет", minutes(oldMin), r.Params.Profile)
		} else {
			if _, err := fmt.Sscan(t.Value, &newMin); err != nil || newMin-oldMin < r.Params.ThresholdMin {
				continue
			}
			details["after"] = newMin
			h.Severity = r.Severity
			h.Title = fmt.Sprintf("%s: время до %s выросло на %s мин", t.Name, target, minutes(newMin-oldMin))
			h.Summary = fmt.Sprintf("%s → %s мин, профиль %s", minutes(oldMin), minutes(newMin), r.Params.Profile)
		}
		h.Key = fmt.Sprintf("acc:%s:%s:%s", id, old.Value, t.Value)
		h.Details = details
		hits = append(hits, h)
	}
	return hits, now, nil
}

func minutes(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", math.Round(v*10)/10), ".", ",", 1)
}

var facilityTitles = map[string]string{
	"warehouse": "Склад", "logistics_center": "Логистический центр", "terminal": "Грузовой терминал",
	"sorting_center": "Сортировочный центр", "cold_storage": "Холодильный склад", "wholesale": "Оптовая торговля", "port": "Речной порт",
}

// facilityHits — объекты, впервые появившиеся в реестре после создания правила.
func facilityHits(ctx context.Context, tx pgx.Tx, r *rule) ([]hit, error) {
	rows, err := tx.Query(ctx, `
		SELECT f.id, f.type_code, coalesce(f.name, ''), coalesce(f.official_address, f.address, ''),
			ST_AsGeoJSON(f.point, 6), coalesce(f.floor_area_m2, f.building_area_m2)
		FROM infra.facility f
		JOIN alerting.rule ru ON ru.id = $1
		WHERE f.first_seen_at > $2
		  AND (ru.area IS NULL OR ST_Intersects(f.point, ru.area))
		  AND (cardinality(coalesce($3::text[], '{}')) = 0 OR f.type_code = ANY($3))
		  AND NOT EXISTS (SELECT 1 FROM alerting.hit h WHERE h.rule_id = $1 AND h.key = 'fac:' || f.id)
		ORDER BY f.first_seen_at LIMIT $4`, r.ID, r.CreatedAt, r.Params.FacilityTypes, hitsPerCheck)
	if err != nil {
		return nil, fmt.Errorf("new facilities: %w", err)
	}
	defer rows.Close()
	var hits []hit
	for rows.Next() {
		var id uuid.UUID
		var typ, name, address, geom string
		var floor *float64
		if err := rows.Scan(&id, &typ, &name, &address, &geom, &floor); err != nil {
			return nil, err
		}
		title := name
		if title == "" {
			title = facilityTitles[typ]
		}
		hits = append(hits, hit{
			Key: "fac:" + id.String(), Entity: "facility:" + id.String(), Severity: r.Severity,
			Title:    fmt.Sprintf("Новый объект: %s", title),
			Summary:  strings.TrimSpace(facilityTitles[typ] + ", " + address),
			Details:  map[string]any{"facilityId": id, "type": typ, "name": name, "address": address, "floorAreaM2": floor},
			Geometry: geom,
		})
	}
	return hits, rows.Err()
}

// sourceHits — неудачные загрузки и источники без успешной загрузки дольше порога; источник,
// снова загрузившийся успешно, закрывает свой алерт.
func sourceHits(ctx context.Context, tx pgx.Tx, r *rule) ([]hit, []string, error) {
	rows, err := tx.Query(ctx, `
		SELECT 'run:' || ru.id, ru.source_code, s.title, ru.finished_at, coalesce(ru.error, '')
		FROM ingest.run ru JOIN ingest.source s ON s.code = ru.source_code
		WHERE ru.status = 'failed' AND ru.finished_at > $1
		  AND (cardinality(coalesce($2::text[], '{}')) = 0 OR ru.source_code = ANY($2))
		  AND NOT EXISTS (SELECT 1 FROM alerting.hit h WHERE h.rule_id = $3 AND h.key = 'run:' || ru.id)
		ORDER BY ru.finished_at LIMIT $4`, r.CreatedAt, r.Params.Sources, r.ID, hitsPerCheck)
	if err != nil {
		return nil, nil, fmt.Errorf("failed runs: %w", err)
	}
	var hits []hit
	for rows.Next() {
		var key, code, title, msg string
		var at time.Time
		if err := rows.Scan(&key, &code, &title, &at, &msg); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if len([]rune(msg)) > 300 {
			msg = string([]rune(msg)[:300]) + "…"
		}
		hits = append(hits, hit{
			Key: key, Entity: "source:" + code, Severity: maxSeverity(r.Severity, SeverityWarning),
			Title: "Сбой загрузки: " + title, Summary: msg,
			Details: map[string]any{"source": code, "runId": strings.TrimPrefix(key, "run:"), "finishedAt": at},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `
		SELECT s.code, s.title, max(ru.finished_at), s.interval_s
		FROM ingest.source s
		JOIN ingest.run ru ON ru.source_code = s.code AND ru.status IN ('succeeded', 'unchanged')
		WHERE s.enabled AND s.interval_s IS NOT NULL
		  AND (cardinality(coalesce($1::text[], '{}')) = 0 OR s.code = ANY($1))
		GROUP BY s.code, s.title, s.interval_s
		HAVING max(ru.finished_at) < now() - make_interval(secs => s.interval_s * $2)`, r.Params.Sources, r.Params.StaleFactor)
	if err != nil {
		return nil, nil, fmt.Errorf("stale sources: %w", err)
	}
	for rows.Next() {
		var code, title string
		var last time.Time
		var interval int64
		if err := rows.Scan(&code, &title, &last, &interval); err != nil {
			rows.Close()
			return nil, nil, err
		}
		days := time.Since(last).Hours() / 24
		hits = append(hits, hit{
			Key: "stale:" + code + ":" + last.UTC().Format(time.RFC3339), Entity: "source:" + code, Severity: r.Severity,
			Title:   "Данные устарели: " + title,
			Summary: fmt.Sprintf("Последняя успешная загрузка %s (%s дн. назад), интервал обновления %s дн.", last.Format("02.01.2006"), minutes(days), minutes(float64(interval)/86400)),
			Details: map[string]any{"source": code, "lastSuccess": last, "intervalS": interval},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	resolve, err := entities(ctx, tx, `
		SELECT a.entity FROM alerting.alert a
		WHERE a.rule_id = $1 AND a.status <> 'closed' AND a.entity LIKE 'source:%'
		  AND EXISTS (SELECT 1 FROM ingest.run ru JOIN ingest.source s ON s.code = ru.source_code
			WHERE 'source:' || ru.source_code = a.entity AND ru.status IN ('succeeded', 'unchanged')
			  AND ru.finished_at > coalesce((SELECT max(f.finished_at) FROM ingest.run f
				WHERE f.source_code = ru.source_code AND f.status = 'failed'), '-infinity')
			  AND (s.interval_s IS NULL OR ru.finished_at > now() - make_interval(secs => s.interval_s * $2)))`,
		r.ID, r.Params.StaleFactor)
	return hits, resolve, err
}

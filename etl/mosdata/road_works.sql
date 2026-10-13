-- Дорожные работы → operations.road_work: плановый ремонт (62101; история 60601, 61581, 2468,
-- 3141), аварийно-восстановительный ремонт (62461), ограничения движения на период работ (2527).
-- Жизненный цикл: новая запись — «появилась», изменились содержательные поля — «изменилась»,
-- запись исчезла с портала — «снята»; состояния по датам — operations.refresh_work_states().

CREATE TEMP TABLE src ON COMMIT DROP AS
WITH r AS (
    SELECT r.global_id, r.cells c, mos_stage.geom(r.geometry) AS geom, current_setting('ff.dataset')::int AS ds
    FROM mos_stage.record r
    WHERE r.dataset = current_setting('ff.dataset')::int
), m AS (
    SELECT r.global_id, r.geom,
           CASE r.ds WHEN 62461 THEN 'emergency' WHEN 2527 THEN 'closure' ELSE 'repair' END AS kind,
           CASE r.ds
               WHEN 62461 THEN concat_ws(': ', coalesce(mos_stage.txt(c->'EngineeringNetObj'), mos_stage.txt(c->'OtherNetObj'), 'Аварийный ремонт'),
                                         mos_stage.txt(c->'DamageType'))
               WHEN 2527 THEN coalesce(mos_stage.txt(c->'Name'), mos_stage.txt(c->'Event_type'), 'Ограничение движения')
               ELSE coalesce(mos_stage.txt(c->'WorksType'), mos_stage.txt(c->'WorkType'), 'Ремонтные работы')
           END AS title,
           coalesce(mos_stage.txt(c->'WorksPlace'), mos_stage.txt(c->'Location'), mos_stage.txt(c->'WorkPlaceDescription'),
                    mos_stage.txt(c->'Address'), mos_stage.txt(c->'AddressOfNearbyBuilding')) AS place,
           mos_stage.txt(c->'AdmArea') AS adm_area,
           mos_stage.txt(c->'District') AS district,
           coalesce(mos_stage.txt(c->'WorksStatus'), mos_stage.txt(c->'WorkStatus')) AS source_status,
           mos_stage.date(coalesce(c->>'WorksBeginDate', c->>'WorkStartDate')) AS planned_start,
           mos_stage.date(coalesce(c->>'PlannedEndDate', c->>'WorksEndDate', c->>'WorkEndDate')) AS planned_end,
           CASE WHEN r.ds IN (2468, 3141) THEN mos_stage.date(coalesce(c->>'WorksBeginDate', c->>'WorkStartDate'))
                ELSE mos_stage.date(c->>'ActualBeginDate') END AS actual_start,
           CASE WHEN r.ds IN (2468, 3141) THEN mos_stage.date(coalesce(c->>'WorksEndDate', c->>'WorkEndDate'))
                ELSE mos_stage.date(c->>'ActualEndDate') END AS actual_end,
           mos_stage.ts(c->>'TS_from') AS closure_from,
           mos_stage.ts(c->>'TS_to') AS closure_to,
           coalesce(mos_stage.txt(c->'Customer'), mos_stage.txt(c->'Balanceholder'), mos_stage.txt(c->'Requester')) AS customer,
           coalesce(mos_stage.txt(c->'Contractor'), mos_stage.txt(c->'LeadOfWork')) AS contractor,
           r.ds = 2527 OR (r.ds = 62461 AND coalesce(mos_stage.txt(c->'WorkPlace'), '') ~* 'проезж') AS closure,
           mos_stage.txt(c->'Lanes_closed') AS lanes_closed,
           jsonb_strip_nulls(jsonb_build_object(
               'reason', mos_stage.txt(c->'WorkReason'), 'year', c->'WorkYear',
               'notice', mos_stage.txt(c->'EmCallRegNum'), 'notice_date', mos_stage.txt(c->'EmCallDate'),
               'work_place', mos_stage.txt(c->'WorkPlace'), 'emergency', mos_stage.txt(c->'SignOfEmergency'),
               'urgent', mos_stage.txt(c->'IsCrashSignOfEmergency'), 'network', mos_stage.txt(c->'EngineeringNetObj'),
               'pipe_diameter_mm', c->'PipeDiameter', 'voltage_kv', c->'Voltage', 'pressure_mpa', c->'Pressure',
               'event_type', mos_stage.txt(c->'Event_type'), 'description', mos_stage.txt(c->'Descr'),
               'emergency_description', mos_stage.txt(c->'EmergencyDescription')
           )) AS attrs
    FROM r
), b AS (
    SELECT m.*,
           CASE WHEN kind = 'closure' THEN closure_from
                ELSE coalesce(actual_start, planned_start)::timestamp AT TIME ZONE 'Europe/Moscow' END AS p_start,
           CASE WHEN kind = 'closure' THEN closure_to
                ELSE (coalesce(actual_end, planned_end) + 1)::timestamp AT TIME ZONE 'Europe/Moscow' END AS p_end
    FROM m
)
SELECT b.*,
       -- Конец раньше начала (ошибка в записи) — интервал без конца, чтобы не терять работу.
       CASE WHEN p_end < p_start THEN tstzrange(p_start, NULL, '[)') ELSE tstzrange(p_start, p_end, '[)') END AS period,
       md5(concat_ws('|', title, place, source_status, planned_start, planned_end, actual_start, actual_end,
                     closure_from, closure_to, customer, contractor, lanes_closed, ST_AsEWKB(geom)::text)) AS content_hash
FROM b;

CREATE TEMP TABLE before ON COMMIT DROP AS
SELECT id, mos_global_id, content_hash, removed_at FROM operations.road_work
WHERE mos_dataset = current_setting('ff.dataset')::int;

INSERT INTO operations.road_work (id, mos_dataset, mos_global_id, kind, title, place, adm_area, district, source_status,
                                  planned_start, planned_end, actual_start, actual_end, period, customer, contractor,
                                  closure, lanes_closed, attrs, geom, state, content_hash, source_code, snapshot_id,
                                  first_seen_at, last_seen_at, changed_at)
SELECT uuidv7(), current_setting('ff.dataset')::int, global_id, kind, title, place, adm_area, district, source_status,
       planned_start, planned_end, actual_start, actual_end, period, customer, contractor, closure, lanes_closed, attrs,
       geom, operations.work_state(period, source_status, false), content_hash,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now(), now()
FROM src
ON CONFLICT (mos_dataset, mos_global_id) DO UPDATE SET
    kind = EXCLUDED.kind, title = EXCLUDED.title, place = EXCLUDED.place, adm_area = EXCLUDED.adm_area,
    district = EXCLUDED.district, source_status = EXCLUDED.source_status, planned_start = EXCLUDED.planned_start,
    planned_end = EXCLUDED.planned_end, actual_start = EXCLUDED.actual_start, actual_end = EXCLUDED.actual_end,
    period = EXCLUDED.period, customer = EXCLUDED.customer, contractor = EXCLUDED.contractor,
    closure = EXCLUDED.closure, lanes_closed = EXCLUDED.lanes_closed, attrs = EXCLUDED.attrs, geom = EXCLUDED.geom,
    content_hash = EXCLUDED.content_hash, snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now(),
    -- Вернувшаяся запись сразу получает состояние по датам: событие о ней — «появилась».
    state = CASE WHEN operations.road_work.removed_at IS NOT NULL THEN EXCLUDED.state ELSE operations.road_work.state END,
    removed_at = NULL,
    changed_at = CASE WHEN operations.road_work.content_hash <> EXCLUDED.content_hash
                       OR operations.road_work.removed_at IS NOT NULL
                      THEN now() ELSE operations.road_work.changed_at END;

-- События загрузки: новые, вернувшиеся, изменившиеся, снятые с портала.
INSERT INTO operations.road_work_event (work_id, type, details)
SELECT w.id, 'appeared', jsonb_build_object('state', w.state)
FROM operations.road_work w
LEFT JOIN before b ON b.id = w.id
WHERE w.mos_dataset = current_setting('ff.dataset')::int AND w.last_seen_at = now()
  AND (b.id IS NULL OR b.removed_at IS NOT NULL);

INSERT INTO operations.road_work_event (work_id, type, details)
SELECT w.id, 'changed', jsonb_build_object('state', w.state)
FROM operations.road_work w
JOIN before b ON b.id = w.id AND b.removed_at IS NULL
WHERE w.last_seen_at = now() AND b.content_hash <> w.content_hash;

UPDATE operations.road_work SET removed_at = now(), changed_at = now()
WHERE mos_dataset = current_setting('ff.dataset')::int AND last_seen_at < now() AND removed_at IS NULL;

CREATE TEMP TABLE transitions ON COMMIT DROP AS SELECT operations.refresh_work_states() AS n;

-- Влияние: для ещё не завершённых работ с геометрией.
CREATE TEMP TABLE affected ON COMMIT DROP AS
SELECT id, geom, closure, kind FROM operations.road_work
WHERE mos_dataset = current_setting('ff.dataset')::int AND state IN ('planned', 'active') AND geom IS NOT NULL;

DELETE FROM operations.road_work_impact WHERE work_id IN (SELECT id FROM affected);
DELETE FROM operations.road_work_facility WHERE work_id IN (SELECT id FROM affected);

INSERT INTO operations.road_work_impact (work_id, segment_id, class, bridge, length_m)
SELECT a.id, s.id, s.class, s.bridge, s.length_m
FROM affected a
JOIN network.road_segment s ON s.geom && ST_Expand(a.geom, 0.0003)
WHERE ST_DWithin(s.geom::geography, a.geom::geography, 15);

INSERT INTO operations.road_work_facility (work_id, facility_id, distance_m)
SELECT a.id, f.id, ST_Distance(f.point::geography, a.geom::geography)
FROM affected a
JOIN infra.facility f ON f.point && ST_Expand(a.geom, 0.009) AND f.status = 'active'
WHERE ST_DWithin(f.point::geography, a.geom::geography, 500);

-- Значимость: магистрали, главные улицы и мосты — высокая при перекрытии, средняя без него;
-- второстепенные и местные — средняя при перекрытии; остальное — низкая.
UPDATE operations.road_work w SET significance = CASE
        WHEN x.major AND w.closure THEN 'high'
        WHEN x.major OR (x.secondary AND w.closure) THEN 'medium'
        ELSE 'low'
    END
FROM (
    SELECT a.id,
           coalesce(bool_or(i.class IN ('motorway', 'trunk', 'primary') OR i.bridge), false) AS major,
           coalesce(bool_or(i.class IN ('secondary', 'tertiary')), false) AS secondary
    FROM affected a
    LEFT JOIN operations.road_work_impact i ON i.work_id = a.id
    GROUP BY a.id
) x
WHERE w.id = x.id;

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'works' AS key, count(*) AS value FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND removed_at IS NULL
UNION ALL SELECT 'works_active', count(*) FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND state = 'active'
UNION ALL SELECT 'works_planned', count(*) FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND state = 'planned'
UNION ALL SELECT 'works_appeared', count(*) FROM operations.road_work_event e JOIN operations.road_work w ON w.id = e.work_id
          WHERE w.mos_dataset = current_setting('ff.dataset')::int AND e.at = now() AND e.type = 'appeared'
UNION ALL SELECT 'works_changed', count(*) FROM operations.road_work_event e JOIN operations.road_work w ON w.id = e.work_id
          WHERE w.mos_dataset = current_setting('ff.dataset')::int AND e.at = now() AND e.type = 'changed'
UNION ALL SELECT 'works_removed', count(*) FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND removed_at = now()
UNION ALL SELECT 'works_without_geometry', count(*) FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND geom IS NULL
UNION ALL SELECT 'works_without_geometry_pct', coalesce(round(100.0 * count(*) FILTER (WHERE geom IS NULL) / nullif(count(*), 0)), 0)
          FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND removed_at IS NULL
UNION ALL SELECT 'works_high', count(*) FROM operations.road_work WHERE mos_dataset = current_setting('ff.dataset')::int AND state IN ('planned', 'active') AND significance = 'high'
UNION ALL SELECT 'state_transitions', n FROM transitions;

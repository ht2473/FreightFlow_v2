-- Знаки → ограничения: таблички к знакам, привязка знаков к участкам сети, ограничения из
-- знаков, расхождения с OSM, объединённые ограничения по приоритету «ручное > знак > OSM».

-- +goose Up

-- Таблички, отнесённые к знаку (в пределах 3 м от него).
ALTER TABLE network.road_sign ADD COLUMN plates text[] NOT NULL DEFAULT '{}';

-- Расхождения официальных знаков и разметки OSM.
CREATE TABLE network.restriction_conflict (
    id           uuid PRIMARY KEY,
    segment_id   uuid NOT NULL REFERENCES network.road_segment (id) ON DELETE CASCADE,
    kind         text NOT NULL,
    type         text NOT NULL,
    sign_id      uuid REFERENCES network.road_sign (id) ON DELETE CASCADE,
    details      jsonb NOT NULL DEFAULT '{}'::jsonb,
    detected_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT restriction_conflict_type_chk CHECK (type IN ('sign_not_in_osm', 'osm_not_signed'))
);

COMMENT ON COLUMN network.restriction_conflict.type IS
    'sign_not_in_osm — знак есть, в OSM ограничения этого вида на участке нет; '
    'osm_not_signed — в OSM ограничение есть, знака этого вида рядом с участком нет';

CREATE INDEX restriction_conflict_segment_idx ON network.restriction_conflict (segment_id);
CREATE INDEX restriction_conflict_type_idx ON network.restriction_conflict (type, kind);

-- Действие знака для грузового движения с учётом табличек: вид ограничения или NULL, если
-- знак к грузовикам не относится (табличка другого вида ТС, исключение для грузовых).
-- 8.4.1 — грузовые автомобили; 8.4.9 — «не распространяется на грузовые»; 8.4.2–8.4.8,
-- 8.4.10–8.4.15 — другие виды ТС или исключения, не меняющие действие для грузовиков.
-- +goose StatementBegin
CREATE FUNCTION network.sign_restriction(code text, plates text[]) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN '8.4.9' = ANY (plates) THEN NULL
        WHEN code = '3.1' THEN CASE WHEN '8.4.1' = ANY (plates) THEN 'hgv_ban' END
        WHEN code IN ('3.2', '3.3') AND '8.4.1' = ANY (plates) THEN 'hgv_ban'
        WHEN EXISTS (SELECT 1 FROM unnest(plates) p WHERE p ~ '^8\.4\.[2-8]$') AND NOT '8.4.1' = ANY (plates) THEN NULL
        ELSE (SELECT t.restriction_kind FROM ref.road_sign_type t WHERE t.code = sign_restriction.code)
    END
$$;
-- +goose StatementEnd

-- Привязка знаков ограничений к сети и пересборка ограничений из знаков и расхождений.
-- Знак — к ближайшему участку в 25 м; второй участок с другим названием не дальше 5 м сверх
-- ближайшего — привязка неоднозначна (достоверность 0,4 против 0,6). Направление знака в наборе
-- не указано: ограничение действует в обе стороны на весь участок OSM. Возвращает число
-- привязанных знаков.
-- +goose StatementBegin
CREATE FUNCTION network.match_signs() RETURNS integer
    LANGUAGE plpgsql AS $$
DECLARE
    n integer;
BEGIN
    -- Таблички — к ближайшему основному знаку не дальше 3 м.
    UPDATE network.road_sign s SET plates = coalesce(p.plates, '{}')
    FROM (
        SELECT m.id, array_agg(DISTINCT pl.code ORDER BY pl.code) AS plates
        FROM network.road_sign m
        JOIN network.road_sign pl ON pl.status = 'active' AND pl.code LIKE '8.%'
         AND pl.point && ST_Expand(m.point, 0.00005)
         AND ST_DWithin(pl.point::geography, m.point::geography, 3)
        WHERE m.status = 'active' AND m.code LIKE '3.%'
        GROUP BY m.id
    ) p
    WHERE s.id = p.id AND s.plates IS DISTINCT FROM p.plates;

    DROP TABLE IF EXISTS sign_kind, sign_match;
    CREATE TEMP TABLE sign_kind ON COMMIT DROP AS
    SELECT s.id, s.point, s.code, s.plates, s.sign_type, network.sign_restriction(s.code, s.plates) AS kind
    FROM network.road_sign s
    WHERE s.status = 'active' AND s.code LIKE '3.%';
    DELETE FROM sign_kind WHERE kind IS NULL;

    CREATE TEMP TABLE sign_match ON COMMIT DROP AS
    SELECT k.id AS sign_id, c.segment_id, c.d, c.second_d, c.second_other
    FROM sign_kind k
    CROSS JOIN LATERAL (
        SELECT x.id AS segment_id, x.d,
               lead(x.d) OVER (ORDER BY x.d) AS second_d,
               lead(coalesce(x.name, '')) OVER (ORDER BY x.d) IS DISTINCT FROM coalesce(x.name, '') AS second_other
        FROM (
            SELECT r.id, r.name, ST_Distance(r.geom::geography, k.point::geography) AS d
            FROM network.road_segment r
            WHERE r.geom && ST_Expand(k.point, 0.0004) AND ST_DWithin(r.geom::geography, k.point::geography, 25)
            ORDER BY d
            LIMIT 2
        ) x
        ORDER BY x.d
        LIMIT 1
    ) c;

    UPDATE network.road_sign s SET segment_id = NULL, match_distance_m = NULL,
        match_status = CASE WHEN network.sign_restriction(s.code, s.plates) IS NULL THEN 'pending' ELSE 'unmatched' END
    WHERE s.status = 'active' AND s.code LIKE '3.%' AND NOT EXISTS (SELECT 1 FROM sign_match m WHERE m.sign_id = s.id);

    UPDATE network.road_sign s SET segment_id = m.segment_id, match_distance_m = m.d,
        match_status = CASE WHEN m.second_d IS NOT NULL AND m.second_d - m.d <= 5 AND m.second_other THEN 'ambiguous' ELSE 'matched' END
    FROM sign_match m WHERE m.sign_id = s.id;
    GET DIAGNOSTICS n = ROW_COUNT;

    -- Ограничения из знаков.
    DELETE FROM network.restriction WHERE origin = 'sign';
    INSERT INTO network.restriction (id, segment_id, kind, direction, condition, raw, origin, confidence, sign_id,
                                     source_code, snapshot_id)
    SELECT uuidv7(), s.segment_id, k.kind, 0,
           nullif(concat_ws('; ',
               CASE WHEN k.code = '3.1' THEN 'въезд' END,
               CASE WHEN '8.11' = ANY (k.plates) THEN 'по разрешённой максимальной массе (табличка 8.11)' END,
               (SELECT 'время по табличке ' || string_agg(p, ', ') FROM unnest(k.plates) p WHERE p LIKE '8.5.%')), ''),
           k.sign_type || CASE WHEN cardinality(k.plates) > 0 THEN ' + ' || array_to_string(k.plates, ', ') ELSE '' END,
           'sign', CASE s.match_status WHEN 'matched' THEN 0.6 ELSE 0.4 END, s.id, s.source_code, s.snapshot_id
    FROM sign_kind k
    JOIN network.road_sign s ON s.id = k.id
    WHERE s.segment_id IS NOT NULL;

    -- Расхождения: знак без ограничения того же вида в OSM на участке.
    DELETE FROM network.restriction_conflict;
    INSERT INTO network.restriction_conflict (id, segment_id, kind, type, sign_id, details)
    SELECT uuidv7(), r.segment_id, r.kind, 'sign_not_in_osm', r.sign_id,
           jsonb_build_object('sign', r.raw, 'osm', (SELECT jsonb_agg(o.raw) FROM network.restriction o
                                                     WHERE o.segment_id = r.segment_id AND o.origin = 'osm'))
    FROM network.restriction r
    WHERE r.origin = 'sign'
      AND NOT EXISTS (
          SELECT 1 FROM network.restriction o
          WHERE o.segment_id = r.segment_id AND o.origin = 'osm'
            AND (o.kind = r.kind OR r.kind = 'hgv_ban' AND o.kind = 'motor_vehicle_ban'));

    -- Расхождения: запрет грузовиков или предел массы и высоты в OSM без знака того же вида в
    -- 40 м от участка (только в Москве — там действует реестр знаков).
    INSERT INTO network.restriction_conflict (id, segment_id, kind, type, details)
    SELECT uuidv7(), o.segment_id, o.kind, 'osm_not_signed', jsonb_build_object('osm', o.raw, 'condition', o.condition)
    FROM network.restriction o
    JOIN network.road_segment g ON g.id = o.segment_id
    JOIN territory.admin_area m ON m.kind = 'region' AND m.name = 'Москва' AND ST_Intersects(m.geom, g.geom)
    WHERE o.origin = 'osm' AND o.kind IN ('hgv_ban', 'maxweight', 'maxheight')
      AND NOT EXISTS (
          SELECT 1 FROM sign_kind k
          WHERE k.kind = o.kind AND k.point && ST_Expand(g.geom, 0.0007)
            AND ST_DWithin(k.point::geography, g.geom::geography, 40));

    RETURN n;
END
$$;
-- +goose StatementEnd

-- Объединённые ограничения участка: для каждого вида — запись с наивысшим приоритетом
-- источника (ручное > знак > OSM); величина — из ручного уточнения или OSM (в наборе знаков
-- величин нет).
CREATE VIEW network.restriction_effective AS
SELECT DISTINCT ON (r.segment_id, r.kind)
       r.segment_id, r.kind, r.origin, r.direction, r.condition, r.confidence,
       coalesce(r.value, (SELECT o.value FROM network.restriction o
                          WHERE o.segment_id = r.segment_id AND o.kind = r.kind AND o.value IS NOT NULL
                          ORDER BY CASE o.origin WHEN 'manual' THEN 1 WHEN 'osm' THEN 2 ELSE 3 END LIMIT 1)) AS value,
       (SELECT array_agg(DISTINCT o.origin) FROM network.restriction o
        WHERE o.segment_id = r.segment_id AND o.kind = r.kind) AS origins
FROM network.restriction r
ORDER BY r.segment_id, r.kind, CASE r.origin WHEN 'manual' THEN 1 WHEN 'sign' THEN 2 ELSE 3 END, r.confidence DESC;

-- Действует ли ограничение по времени: часы в условии OSM или табличка 8.5.x у знака.
CREATE FUNCTION network.time_based(cond text) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN coalesce(cond ~ '([0-9]{1,2}:[0-9]{2}|8\.5\.)', false);

-- Тайлы ограничений: сводка по участку из объединённых ограничений с источниками.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.restrictions(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'restrictions', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT s.osm_id AS fid, s.id::text AS id, s.name,
               CASE WHEN bool_or(r.kind IN ('hgv_ban', 'motor_vehicle_ban', 'hazmat_ban') AND NOT network.time_based(r.condition)) THEN 'ban'
                    WHEN bool_or(r.kind IN ('maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength')) THEN 'limit'
                    WHEN bool_or(r.kind IN ('hgv_ban', 'motor_vehicle_ban', 'hazmat_ban')) THEN 'ban'
                    ELSE 'access' END AS severity,
               bool_and(network.time_based(r.condition)) AS conditional,
               string_agg(DISTINCT r.kind, ',') AS kinds,
               string_agg(DISTINCT array_to_string(r.origins, ','), ';') AS origins,
               bool_or('sign' = ANY (r.origins)) AS signed,
               min(r.value) FILTER (WHERE r.kind = 'maxweight')::float8 AS maxweight,
               min(r.value) FILTER (WHERE r.kind = 'maxaxleload')::float8 AS maxaxleload,
               min(r.value) FILTER (WHERE r.kind = 'maxheight')::float8 AS maxheight,
               min(r.value) FILTER (WHERE r.kind = 'maxwidth')::float8 AS maxwidth,
               string_agg(DISTINCT r.condition, '; ') AS conditions,
               ST_AsMVTGeom(s.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.restriction_effective r
        JOIN network.road_segment s ON s.id = r.segment_id
        WHERE s.geom_3857 && ST_TileEnvelope(z, x, y)
        GROUP BY s.id
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Расхождения знаков и OSM — линии участков с видом расхождения.
-- +goose StatementBegin
CREATE FUNCTION tiles.restriction_conflicts(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'restriction_conflicts', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT s.osm_id AS fid, s.id::text AS id, s.name, min(c.type) AS type, string_agg(DISTINCT c.kind, ',') AS kinds,
               ST_AsMVTGeom(s.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.restriction_conflict c
        JOIN network.road_segment s ON s.id = c.segment_id
        WHERE s.geom_3857 && ST_TileEnvelope(z, x, y)
        GROUP BY s.id
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION tiles.restriction_conflicts(integer, integer, integer);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.restrictions(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'restrictions', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT s.osm_id AS fid, s.id::text AS id, s.name,
               CASE WHEN bool_or(r.kind IN ('hgv_ban', 'motor_vehicle_ban') AND r.condition IS NULL) THEN 'ban'
                    WHEN bool_or(r.kind IN ('maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength')) THEN 'limit'
                    WHEN bool_or(r.kind IN ('hgv_ban', 'motor_vehicle_ban')) THEN 'ban'
                    ELSE 'access' END AS severity,
               bool_and(r.condition IS NOT NULL) AS conditional,
               string_agg(DISTINCT r.kind, ',') AS kinds,
               min(r.value) FILTER (WHERE r.kind = 'maxweight')::float8 AS maxweight,
               min(r.value) FILTER (WHERE r.kind = 'maxaxleload')::float8 AS maxaxleload,
               min(r.value) FILTER (WHERE r.kind = 'maxheight')::float8 AS maxheight,
               min(r.value) FILTER (WHERE r.kind = 'maxwidth')::float8 AS maxwidth,
               string_agg(DISTINCT r.condition, '; ') AS conditions,
               ST_AsMVTGeom(s.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.restriction r
        JOIN network.road_segment s ON s.id = r.segment_id
        WHERE s.geom_3857 && ST_TileEnvelope(z, x, y)
        GROUP BY s.id
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

DROP FUNCTION network.time_based(text);
DROP VIEW network.restriction_effective;
DROP FUNCTION network.match_signs();
DROP FUNCTION network.sign_restriction(text, text[]);
DROP TABLE network.restriction_conflict;
ALTER TABLE network.road_sign DROP COLUMN plates;

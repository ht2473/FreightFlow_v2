-- Дорожные знаки (набор 62681) → network.road_sign и справочник типов знаков.
-- Параметры транзакции: ff.source, ff.snapshot, ff.dataset.

CREATE TEMP TABLE src ON COMMIT DROP AS
SELECT r.global_id,
       mos_stage.num(r.cells->'ID')::bigint AS mos_id,
       mos_stage.txt(r.cells->'SignType') AS sign_type,
       substring(mos_stage.txt(r.cells->'SignType') FROM '^(\d+(?:\.\d+){1,3})') AS code,
       CASE lower(mos_stage.txt(r.cells->'SignIsTemporary')) WHEN 'да' THEN true WHEN 'нет' THEN false END AS temporary,
       mos_stage.date(r.cells->>'DateOfPlacing') AS placed_on,
       mos_stage.txt(r.cells->'Location') AS location,
       mos_stage.txt(r.cells->'AdmArea') AS adm_area,
       mos_stage.txt(r.cells->'District') AS district,
       coalesce(
           (SELECT CASE WHEN GeometryType(g) = 'POINT' THEN g ELSE ST_PointOnSurface(g) END
            FROM mos_stage.geom(r.geometry) g WHERE g IS NOT NULL),
           ST_SetSRID(ST_MakePoint(mos_stage.num(r.cells->'Longitude_WGS84')::float8,
                                   mos_stage.num(r.cells->'Latitude_WGS84')::float8), 4326)
       ) AS point
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int;

DELETE FROM src WHERE sign_type IS NULL OR point IS NULL OR ST_IsEmpty(point);

-- Названия знаков — как в наборе (самое частое написание для номера).
INSERT INTO ref.road_sign_type (code, name, sign_group)
SELECT DISTINCT ON (code) code, trim(substr(sign_type, length(code) + 1)), split_part(code, '.', 1)::smallint
FROM (SELECT code, sign_type, count(*) AS n FROM src WHERE code IS NOT NULL GROUP BY code, sign_type) t
WHERE trim(substr(sign_type, length(code) + 1)) <> ''
ORDER BY code, n DESC
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name;

INSERT INTO network.road_sign (id, mos_global_id, mos_id, code, sign_type, temporary, placed_on, location, adm_area,
                               district, point, source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), global_id, mos_id, code, sign_type, temporary, placed_on, location, adm_area, district, point,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM src
ON CONFLICT (mos_global_id) DO UPDATE SET
    mos_id = EXCLUDED.mos_id, code = EXCLUDED.code, sign_type = EXCLUDED.sign_type, temporary = EXCLUDED.temporary,
    placed_on = EXCLUDED.placed_on, location = EXCLUDED.location, adm_area = EXCLUDED.adm_area,
    district = EXCLUDED.district, point = EXCLUDED.point,
    -- Сдвинутый или заменённый знак привязывается заново.
    match_status = CASE WHEN network.road_sign.point IS DISTINCT FROM EXCLUDED.point
                             OR network.road_sign.code IS DISTINCT FROM EXCLUDED.code
                        THEN 'pending' ELSE network.road_sign.match_status END,
    status = 'active', snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

UPDATE network.road_sign SET status = 'missing'
WHERE source_code = current_setting('ff.source') AND last_seen_at < now() AND status = 'active';

-- Привязка знаков ограничений к сети, ограничения из знаков и расхождения с OSM.
CREATE TEMP TABLE matched ON COMMIT DROP AS SELECT network.match_signs() AS n;

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'signs_active' AS key, count(*) AS value FROM network.road_sign WHERE status = 'active'
UNION ALL SELECT 'signs_new', count(*) FROM network.road_sign WHERE first_seen_at = now()
UNION ALL SELECT 'signs_missing', count(*) FROM network.road_sign WHERE status = 'missing' AND last_seen_at < now()
UNION ALL SELECT 'signs_unrecognized', count(*) FROM network.road_sign WHERE status = 'active' AND code IS NULL
UNION ALL SELECT 'restriction_signs', count(*) FROM network.road_sign s JOIN ref.road_sign_type t USING (code)
          WHERE s.status = 'active' AND t.restriction_kind IS NOT NULL
UNION ALL SELECT 'sign_types', count(DISTINCT code) FROM network.road_sign WHERE status = 'active'
UNION ALL SELECT 'signs_matched', n FROM matched
UNION ALL SELECT 'signs_match_pct', coalesce(round(100.0 * count(*) FILTER (WHERE match_status IN ('matched', 'ambiguous'))
                                                   / nullif(count(*) FILTER (WHERE match_status <> 'pending'), 0)), 0)
          FROM network.road_sign WHERE status = 'active' AND code LIKE '3.%' 
UNION ALL SELECT 'signs_unmatched', count(*) FROM network.road_sign WHERE status = 'active' AND match_status = 'unmatched'
UNION ALL SELECT 'sign_restrictions', count(*) FROM network.restriction WHERE origin = 'sign'
UNION ALL SELECT 'conflicts_sign_not_in_osm', count(*) FROM network.restriction_conflict WHERE type = 'sign_not_in_osm'
UNION ALL SELECT 'conflicts_osm_not_signed', count(*) FROM network.restriction_conflict WHERE type = 'osm_not_signed';

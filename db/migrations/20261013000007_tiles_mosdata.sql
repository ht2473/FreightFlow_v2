-- Тайлы данных портала Москвы: официальные точки (вместо совпавших записей OSM), дорожные
-- работы со сроками для шкалы времени, дорожные знаки, инфраструктура УДС, ограничения для
-- размещения, ввод объектов в эксплуатацию.

-- +goose Up

-- Точечная инфраструктура: официальные записи и записи OSM без официального совпадения.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.poi(z integer, x integer, y integer, layer text, kinds text[]) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, layer, 4096, 'geom', 'fid'), '')
    FROM (
        SELECT CASE WHEN p.osm_id IS NOT NULL THEN tiles.osm_fid(p.osm_type, p.osm_id) ELSE p.mos_global_id * 4 + 3 END AS fid,
               p.id::text AS id, p.kind, p.name,
               CASE WHEN p.mos_global_id IS NOT NULL THEN 'mos' ELSE 'osm' END AS source,
               coalesce(p.address, nullif(concat_ws(', ', p.tags->>'addr:street', p.tags->>'addr:housenumber'), '')) AS address,
               coalesce(p.tags->>'brand', p.tags->>'operator', p.tags->>'Owner') AS operator,
               coalesce(p.tags->>'opening_hours', p.tags->>'OperatingMode') AS opening_hours,
               mos_stage.num(p.tags->'Capacity')::int AS capacity,
               ST_AsMVTGeom(p.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.poi p
        WHERE p.kind = ANY (kinds) AND p.official_id IS NULL
          AND p.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

CREATE OR REPLACE FUNCTION tiles.attractors(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.poi(z, x, y, 'attractors', ARRAY['marketplace', 'fair', 'rail_station', 'tpu', 'technopark']);

-- Дорожные работы: сроки — номера дней от 1970-01-01, чтобы клиент фильтровал по дате шкалы;
-- открытый конец — 999999. Подробности (место, заказчик, исполнитель, статус источника) — с
-- 14-го масштаба, на мелких геометрия упрощается сильнее.
-- +goose StatementBegin
CREATE FUNCTION tiles.road_works(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'road_works', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT w.mos_global_id AS fid, w.id::text AS id, w.kind, w.title, w.state, w.significance, w.closure,
               CASE WHEN z >= 14 THEN w.place END AS place,
               CASE WHEN z >= 14 THEN w.source_status END AS source_status,
               CASE WHEN z >= 14 THEN w.customer END AS customer,
               CASE WHEN z >= 14 THEN w.contractor END AS contractor,
               coalesce((lower(w.period) AT TIME ZONE 'Europe/Moscow')::date - date '1970-01-01', 0) AS start_day,
               coalesce((upper(w.period) AT TIME ZONE 'Europe/Moscow')::date - date '1970-01-01', 999999) AS end_day,
               to_char(lower(w.period) AT TIME ZONE 'Europe/Moscow', 'DD.MM.YYYY') AS starts,
               to_char((upper(w.period) AT TIME ZONE 'Europe/Moscow') - interval '1 day', 'DD.MM.YYYY') AS ends,
               ST_AsMVTGeom(CASE WHEN z < 14 THEN ST_SimplifyPreserveTopology(w.geom_3857, tiles.px(z) * 4) ELSE w.geom_3857 END,
                            ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM operations.road_work w
        WHERE w.removed_at IS NULL AND w.geom_3857 && ST_TileEnvelope(z, x, y)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Дорожные знаки: до 15-го масштаба — только знаки ограничений для грузовиков.
-- +goose StatementBegin
CREATE FUNCTION tiles.road_signs(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'road_signs', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT s.mos_global_id AS fid, s.id::text AS id, s.code, s.sign_type AS name, t.restriction_kind AS kind,
               s.match_status, s.temporary, s.location,
               ST_AsMVTGeom(s.point_3857, ST_TileEnvelope(z, x, y), 4096, 32, true) AS geom
        FROM network.road_sign s
        LEFT JOIN ref.road_sign_type t ON t.code = s.code
        WHERE s.status = 'active' AND s.point_3857 && ST_TileEnvelope(z, x, y, margin => 32.0 / 4096)
          AND (z >= 15 OR t.restriction_kind IS NOT NULL)
    ) t
$$;
-- +goose StatementEnd

-- Инфраструктура УДС (кроме объектов дорожного хозяйства — это контекст, не слой карты).
-- +goose StatementBegin
CREATE FUNCTION tiles.road_assets(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'road_assets', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT a.mos_global_id AS fid, a.id::text AS id, a.kind, a.name, a.address,
               ST_AsMVTGeom(a.geom_3857, ST_TileEnvelope(z, x, y), 4096, 32, true) AS geom
        FROM network.road_asset a
        WHERE a.status = 'active' AND a.kind <> 'road_object'
          AND a.geom_3857 && ST_TileEnvelope(z, x, y, margin => 32.0 / 4096)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Ограничения для размещения: ОКН и парки.
-- +goose StatementBegin
CREATE FUNCTION tiles.constraint_zones(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'constraint_zones', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT c.mos_global_id AS fid, c.id::text AS id, c.kind, c.name, round((c.area_m2 / 10000)::numeric, 1)::float8 AS area_ha,
               ST_AsMVTGeom(CASE WHEN z < 15 THEN ST_SimplifyPreserveTopology(c.geom_3857, tiles.px(z) * 4) ELSE c.geom_3857 END,
                            ST_TileEnvelope(z, x, y), 4096, 8, true) AS geom
        FROM territory.constraint_zone c
        WHERE c.geom_3857 && ST_TileEnvelope(z, x, y)
          AND c.area_m2 >= CASE WHEN z < 12 THEN 20000 WHEN z < 14 THEN 2000 ELSE 0 END
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Ввод объектов в эксплуатацию (места по адресному реестру).
-- +goose StatementBegin
CREATE FUNCTION tiles.commissioning(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'commissioning', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT c.mos_global_id AS fid, c.id::text AS id, c.object_name AS name, c.purpose, c.address,
               to_char(c.permit_date, 'DD.MM.YYYY') AS permit_date, extract(year FROM c.permit_date)::int AS year,
               c.area_m2::float8 AS area, c.logistics,
               ST_AsMVTGeom(ST_Transform(c.point, 3857), ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.commissioning c
        WHERE c.point IS NOT NULL
          AND c.point && ST_Transform(ST_TileEnvelope(z, x, y, margin => 64.0 / 4096), 4326)
    ) t
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION tiles.commissioning(integer, integer, integer);
DROP FUNCTION tiles.constraint_zones(integer, integer, integer);
DROP FUNCTION tiles.road_assets(integer, integer, integer);
DROP FUNCTION tiles.road_signs(integer, integer, integer);
DROP FUNCTION tiles.road_works(integer, integer, integer);

CREATE OR REPLACE FUNCTION tiles.attractors(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.poi(z, x, y, 'attractors', ARRAY['marketplace', 'rail_station']);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.poi(z integer, x integer, y integer, layer text, kinds text[]) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, layer, 4096, 'geom', 'fid'), '')
    FROM (
        SELECT tiles.osm_fid(p.osm_type, p.osm_id) AS fid, p.id::text AS id, p.kind, p.name,
               coalesce(p.tags->>'brand', p.tags->>'operator') AS operator,
               p.tags->>'opening_hours' AS opening_hours,
               ST_AsMVTGeom(p.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.poi p
        WHERE p.kind = ANY (kinds) AND p.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

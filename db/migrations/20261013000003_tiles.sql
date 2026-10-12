-- Векторные тайлы слоёв данных: функции tiles.<слой>(z, x, y) → MVT. Слой в тайле называется
-- кодом слоя из реестра в коде (internal/tiles). Идентификатор объекта в тайле (fid) —
-- целое из идентификатора OSM, свойство id — ключ записи в базе.

-- +goose Up

-- Контур территории объекта в EPSG:3857 для тайлов.
ALTER TABLE infra.facility
    ADD COLUMN territory_3857 geometry(MultiPolygon, 3857)
        GENERATED ALWAYS AS (ST_Transform(territory, 3857)) STORED;

CREATE INDEX facility_territory_3857_idx ON infra.facility USING gist (territory_3857);
CREATE INDEX poi_kind_point_3857_idx ON infra.poi USING gist (point_3857) INCLUDE (kind);

-- Размер пикселя тайла 4096 на масштабе z, м (EPSG:3857): допуск упрощения геометрии.
CREATE FUNCTION tiles.px(z integer) RETURNS double precision
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN 40075016.68557849 / (4096 * power(2, z));

-- Целый идентификатор объекта OSM для тайла: тип в младших битах.
CREATE FUNCTION tiles.osm_fid(osm_type char, osm_id bigint) RETURNS bigint
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN osm_id * 4 + CASE osm_type WHEN 'N' THEN 0 WHEN 'W' THEN 1 ELSE 2 END;

-- Объекты логистики — точки; размер на карте — по площади застройки.
-- +goose StatementBegin
CREATE FUNCTION tiles.facilities(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    -- Крупные снизу, мелкие поверх.
    SELECT coalesce(ST_AsMVT(t, 'facilities', 4096, 'geom', 'fid' ORDER BY t.area DESC NULLS LAST), '')
    FROM (
        SELECT tiles.osm_fid(f.osm_type, f.osm_id) AS fid, f.id::text AS id, f.type_code AS type,
               f.name, round(f.confidence::numeric, 2)::float8 AS confidence,
               f.building_area_m2::float8 AS area, f.buildings,
               ST_AsMVTGeom(f.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.facility f
        WHERE f.status = 'active' AND f.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

-- Контуры объектов: территория, а без неё — здания.
-- +goose StatementBegin
CREATE FUNCTION tiles.facility_areas(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'facility_areas', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT tiles.osm_fid(f.osm_type, f.osm_id) AS fid, f.id::text AS id, f.type_code AS type, f.name,
               ST_AsMVTGeom(coalesce(f.territory_3857, f.footprint_3857), ST_TileEnvelope(z, x, y), 4096, 8, true) AS geom
        FROM infra.facility f
        WHERE f.status = 'active'
          AND (f.territory_3857 && ST_TileEnvelope(z, x, y) OR f.footprint_3857 && ST_TileEnvelope(z, x, y))
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Промзоны; на мелких масштабах контуры упрощаются, а мелкие зоны не выводятся.
-- +goose StatementBegin
CREATE FUNCTION tiles.industrial_zones(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'industrial_zones', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT tiles.osm_fid(i.osm_type, i.osm_id) AS fid, i.id::text AS id, i.name,
               round((i.area_m2 / 10000)::numeric, 1)::float8 AS area_ha,
               ST_AsMVTGeom(CASE WHEN z < 14 THEN ST_SimplifyPreserveTopology(i.geom_3857, tiles.px(z)) ELSE i.geom_3857 END,
                            ST_TileEnvelope(z, x, y), 4096, 8, true) AS geom
        FROM infra.industrial_zone i
        WHERE i.geom_3857 && ST_TileEnvelope(z, x, y)
          AND i.area_m2 >= CASE WHEN z < 11 THEN 50000 WHEN z < 13 THEN 10000 ELSE 0 END
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Дорожная сеть по классам: на мелких масштабах только магистрали, склеенные по классу
-- (без свойств участков), с 13-го масштаба — участки со свойствами.
-- +goose StatementBegin
CREATE FUNCTION tiles.roads(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    WITH env AS (SELECT ST_TileEnvelope(z, x, y) AS e),
    classes AS (
        SELECT CASE
            WHEN z < 10 THEN ARRAY['motorway', 'trunk']
            WHEN z < 12 THEN ARRAY['motorway', 'trunk', 'primary', 'secondary']
            WHEN z < 14 THEN ARRAY['motorway', 'trunk', 'primary', 'secondary', 'tertiary', 'unclassified', 'residential']
            ELSE ARRAY['motorway', 'trunk', 'primary', 'secondary', 'tertiary', 'unclassified', 'residential',
                       'living_street', 'service', 'track']
        END AS list
    )
    SELECT coalesce(ST_AsMVT(t, 'roads', 4096, 'geom'), '')
    FROM (
        SELECT NULL::text AS id, g.class, NULL::text AS name, NULL::text AS ref, NULL::smallint AS oneway,
               ST_AsMVTGeom(ST_Simplify(ST_LineMerge(ST_Collect(g.geom_3857)), tiles.px(z)), env.e, 4096, 16, true) AS geom
        FROM network.road_segment g, env, classes
        WHERE z < 13 AND g.geom_3857 && env.e AND g.class = ANY (classes.list)
        GROUP BY g.class, env.e
        UNION ALL
        SELECT s.id::text, s.class, s.name, s.ref, s.oneway,
               ST_AsMVTGeom(s.geom_3857, env.e, 4096, 16, true)
        FROM network.road_segment s, env, classes
        WHERE z >= 13 AND s.geom_3857 && env.e AND s.class = ANY (classes.list)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Участки с ограничениями для грузовиков: одна линия на участок, сводка ограничений в свойствах.
-- severity: ban — запрет грузовиков или всех ТС, limit — предел массы, нагрузки или габарита,
-- access — только для доступа (hgv=destination/delivery).
-- +goose StatementBegin
CREATE FUNCTION tiles.restrictions(z integer, x integer, y integer) RETURNS bytea
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

-- +goose StatementBegin
CREATE FUNCTION tiles.bridges(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'bridges', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT b.osm_id AS fid, b.id::text AS id, b.name, b.kind, round(b.length_m::numeric)::float8 AS length_m,
               b.maxweight_t::float8 AS maxweight, b.maxheight_m::float8 AS maxheight,
               ST_AsMVTGeom(b.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.bridge b
        WHERE b.geom_3857 && ST_TileEnvelope(z, x, y)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Точечная инфраструктура заданных видов (слои АЗС, стоянок, точек притяжения, грузовых пунктов).
-- +goose StatementBegin
CREATE FUNCTION tiles.poi(z integer, x integer, y integer, layer text, kinds text[]) RETURNS bytea
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

CREATE FUNCTION tiles.fuel_stations(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.poi(z, x, y, 'fuel_stations', ARRAY['fuel']);

CREATE FUNCTION tiles.truck_parking(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.poi(z, x, y, 'truck_parking', ARRAY['truck_parking', 'rest_area']);

CREATE FUNCTION tiles.attractors(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.poi(z, x, y, 'attractors', ARRAY['marketplace', 'rail_station']);

CREATE FUNCTION tiles.freight_points(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.poi(z, x, y, 'freight_points', ARRAY['rail_freight', 'customs', 'port']);

-- Границы Москвы: округа всегда, районы — с 10-го масштаба. Полигоны, чтобы клиент мог
-- подписать территорию и подсветить её.
-- +goose StatementBegin
CREATE FUNCTION tiles.boundaries(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'boundaries', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT a.osm_id AS fid, a.id::text AS id, a.kind, a.name, coalesce(a.short_name, a.name) AS label,
               round(a.area_km2::numeric, 1)::float8 AS area_km2,
               ST_AsMVTGeom(ST_SimplifyPreserveTopology(a.geom_3857, tiles.px(z)), ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM territory.admin_area a
        JOIN territory.admin_area m ON m.kind = 'region' AND m.name = 'Москва'
        WHERE a.geom_3857 && ST_TileEnvelope(z, x, y)
          AND (a.id = m.id OR a.kind = 'okrug' OR a.kind = 'district' AND z >= 10)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Кольца: контур зоны внутри кольца (СК, ТТК, МКАД, ЦКАД).
-- +goose StatementBegin
CREATE FUNCTION tiles.rings(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'rings', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT r.ord AS fid, r.code, r.name, round(r.length_km::numeric, 1)::float8 AS length_km,
               ST_AsMVTGeom(ST_SimplifyPreserveTopology(ST_Boundary(r.zone_3857), tiles.px(z)),
                            ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM territory.ring_zone r
        WHERE r.zone_3857 && ST_TileEnvelope(z, x, y)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION tiles.rings(integer, integer, integer);
DROP FUNCTION tiles.boundaries(integer, integer, integer);
DROP FUNCTION tiles.freight_points(integer, integer, integer);
DROP FUNCTION tiles.attractors(integer, integer, integer);
DROP FUNCTION tiles.truck_parking(integer, integer, integer);
DROP FUNCTION tiles.fuel_stations(integer, integer, integer);
DROP FUNCTION tiles.poi(integer, integer, integer, text, text[]);
DROP FUNCTION tiles.bridges(integer, integer, integer);
DROP FUNCTION tiles.restrictions(integer, integer, integer);
DROP FUNCTION tiles.roads(integer, integer, integer);
DROP FUNCTION tiles.industrial_zones(integer, integer, integer);
DROP FUNCTION tiles.facility_areas(integer, integer, integer);
DROP FUNCTION tiles.facilities(integer, integer, integer);
DROP FUNCTION tiles.osm_fid(char, bigint);
DROP FUNCTION tiles.px(integer);
DROP INDEX infra.poi_kind_point_3857_idx;
DROP INDEX infra.facility_territory_3857_idx;
ALTER TABLE infra.facility DROP COLUMN territory_3857;

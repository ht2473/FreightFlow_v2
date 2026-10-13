-- Доступность объектов реестра: время и расстояние до МКАД, ТТК, центра и ж/д грузовых пунктов
-- для типовых профилей ТС по графу маршрутизации.

-- +goose Up

-- Цели: точки на кольцах (через равные промежутки на основных проезжих частях), центр города,
-- ж/д грузовые пункты.
CREATE TABLE analytics.accessibility_target (
    id    serial PRIMARY KEY,
    kind  text NOT NULL,
    name  text NOT NULL,
    point geometry(Point, 4326) NOT NULL,
    CONSTRAINT accessibility_target_kind_chk CHECK (kind IN ('mkad', 'ttk', 'center', 'rail_freight'))
);

-- Время до ближайшей цели вида для объекта и профиля; minutes пусто — цель недостижима.
CREATE TABLE analytics.accessibility (
    facility_id    uuid NOT NULL REFERENCES infra.facility (id) ON DELETE CASCADE,
    profile_code   text NOT NULL REFERENCES ref.vehicle_profile (code) ON DELETE CASCADE,
    target_kind    text NOT NULL,
    minutes        real,
    km             real,
    target_id      integer REFERENCES analytics.accessibility_target (id) ON DELETE SET NULL,
    graph_built_at timestamptz NOT NULL,
    computed_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (facility_id, profile_code, target_kind)
);

COMMENT ON TABLE analytics.accessibility IS
    'физическая доступность по сети: ограничения участков для профиля учитываются, зоны пропусков — нет (как с пропусками всех зон)';

-- Цели по текущим кольцам и реестру точек: МКАД — каждые 3 км, ТТК — каждые 1,5 км по линиям
-- кольца длиннее 10 км (основные проезжие части, без съездов); центр — Красная площадь.
-- +goose StatementBegin
CREATE FUNCTION analytics.refresh_accessibility_targets() RETURNS integer
    LANGUAGE plpgsql AS $$
DECLARE
    n integer;
BEGIN
    DELETE FROM analytics.accessibility_target;
    INSERT INTO analytics.accessibility_target (kind, name, point)
    SELECT r.code, CASE r.code WHEN 'mkad' THEN 'МКАД' ELSE 'ТТК' END,
           (ST_Dump(ST_LineInterpolatePoints(l.geom,
               least(1, CASE r.code WHEN 'mkad' THEN 3000 ELSE 1500 END / ST_Length(l.geom::geography))))).geom
    FROM territory.ring_zone r
    CROSS JOIN LATERAL (SELECT (ST_Dump(ST_LineMerge(r.line))).geom) l
    WHERE r.code IN ('mkad', 'ttk') AND ST_Length(l.geom::geography) > 10000;

    INSERT INTO analytics.accessibility_target (kind, name, point)
    VALUES ('center', 'Центр (Красная площадь)', ST_SetSRID(ST_MakePoint(37.6208, 55.7539), 4326));

    INSERT INTO analytics.accessibility_target (kind, name, point)
    SELECT 'rail_freight', coalesce(p.name, 'Ж/д грузовой пункт'), p.point
    FROM infra.poi p WHERE p.kind = 'rail_freight';

    SELECT count(*) INTO n FROM analytics.accessibility_target;
    RETURN n;
END
$$;
-- +goose StatementEnd

-- Тайлы объектов: время до МКАД, ТТК, центра и ж/д грузового пункта для грузовика 20 т.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.facilities(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    -- Крупные снизу, мелкие поверх.
    SELECT coalesce(ST_AsMVT(t, 'facilities', 4096, 'geom', 'fid' ORDER BY t.area DESC NULLS LAST), '')
    FROM (
        SELECT tiles.osm_fid(f.osm_type, f.osm_id) AS fid, f.id::text AS id, f.type_code AS type,
               f.name, coalesce(f.official_address, f.address) AS address,
               round(f.confidence::numeric, 2)::float8 AS confidence,
               f.building_area_m2::float8 AS area, f.buildings,
               round(a.mkad::numeric)::float8 AS mkad_min, round(a.ttk::numeric)::float8 AS ttk_min,
               round(a.center::numeric)::float8 AS center_min, round(a.rail::numeric)::float8 AS rail_min,
               ST_AsMVTGeom(f.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.facility f
        LEFT JOIN LATERAL (
            SELECT max(minutes) FILTER (WHERE target_kind = 'mkad') AS mkad,
                   max(minutes) FILTER (WHERE target_kind = 'ttk') AS ttk,
                   max(minutes) FILTER (WHERE target_kind = 'center') AS center,
                   max(minutes) FILTER (WHERE target_kind = 'rail_freight') AS rail
            FROM analytics.accessibility WHERE facility_id = f.id AND profile_code = 'truck_20'
        ) a ON true
        WHERE f.status = 'active' AND f.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.facilities(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    -- Крупные снизу, мелкие поверх.
    SELECT coalesce(ST_AsMVT(t, 'facilities', 4096, 'geom', 'fid' ORDER BY t.area DESC NULLS LAST), '')
    FROM (
        SELECT tiles.osm_fid(f.osm_type, f.osm_id) AS fid, f.id::text AS id, f.type_code AS type,
               f.name, coalesce(f.official_address, f.address) AS address,
               round(f.confidence::numeric, 2)::float8 AS confidence,
               f.building_area_m2::float8 AS area, f.buildings,
               ST_AsMVTGeom(f.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.facility f
        WHERE f.status = 'active' AND f.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

DROP FUNCTION analytics.refresh_accessibility_targets();
DROP TABLE analytics.accessibility;
DROP TABLE analytics.accessibility_target;

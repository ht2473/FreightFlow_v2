-- Домен из OpenStreetMap: территории, справочник объектов, объекты и инфраструктура,
-- дорожная сеть. Происхождение записи: source_code, snapshot_id, first_seen_at, last_seen_at.

-- +goose Up

-- Промежуточная схема импорта osm2pgsql (таблицы создаёт и пересоздаёт сам импорт).
CREATE SCHEMA IF NOT EXISTS osm_stage;

-- Территории ---------------------------------------------------------------

CREATE TABLE territory.admin_area (
    id            uuid PRIMARY KEY,
    kind          text NOT NULL,
    admin_level   smallint NOT NULL,
    name          text NOT NULL,
    short_name    text,
    parent_id     uuid REFERENCES territory.admin_area (id) ON DELETE SET NULL,
    osm_id        bigint NOT NULL UNIQUE,
    tags          jsonb NOT NULL DEFAULT '{}'::jsonb,
    geom          geometry(MultiPolygon, 4326) NOT NULL,
    geom_3857     geometry(MultiPolygon, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    area_km2      double precision NOT NULL,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT admin_area_kind_chk CHECK (kind IN ('region', 'okrug', 'municipal_district', 'district'))
);

COMMENT ON COLUMN territory.admin_area.kind IS
    'region — Москва, Московская область; okrug — административный округ Москвы; '
    'municipal_district — городской округ области; district — район или поселение Москвы';

CREATE INDEX admin_area_geom_idx ON territory.admin_area USING gist (geom);
CREATE INDEX admin_area_geom_3857_idx ON territory.admin_area USING gist (geom_3857);
CREATE INDEX admin_area_kind_idx ON territory.admin_area (kind);

-- Кольца Москвы: линия кольца и зона внутри него.
CREATE TABLE territory.ring_zone (
    code          text PRIMARY KEY,
    name          text NOT NULL,
    ord           smallint NOT NULL,
    line          geometry(MultiLineString, 4326) NOT NULL,
    zone          geometry(MultiPolygon, 4326),
    line_3857     geometry(MultiLineString, 3857) GENERATED ALWAYS AS (ST_Transform(line, 3857)) STORED,
    zone_3857     geometry(MultiPolygon, 3857) GENERATED ALWAYS AS (ST_Transform(zone, 3857)) STORED,
    length_km     double precision NOT NULL,
    area_km2      double precision,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ring_zone_code_chk CHECK (code IN ('sk', 'ttk', 'mkad', 'ckad'))
);

CREATE INDEX ring_zone_zone_idx ON territory.ring_zone USING gist (zone);

-- Справочник типов объектов ---------------------------------------------------

CREATE TABLE ref.facility_type (
    code         text PRIMARY KEY,
    name         text NOT NULL,
    description  text NOT NULL,
    icon         text NOT NULL,
    ord          smallint NOT NULL
);

INSERT INTO ref.facility_type (code, name, description, icon, ord) VALUES
    ('logistics_center', 'Логистический центр', 'Логистический или распределительный центр, складской комплекс с несколькими корпусами', 'warehouse', 1),
    ('warehouse', 'Склад', 'Складское здание или складская территория', 'package', 2),
    ('cold_storage', 'Холодильный склад', 'Склад с температурным режимом, холодильник', 'snowflake', 3),
    ('terminal', 'Грузовой терминал', 'Терминал грузовых перевозок, контейнерный терминал', 'container', 4),
    ('sorting_center', 'Сортировочный центр', 'Сортировочный центр почты или курьерской службы', 'mail', 5),
    ('wholesale', 'Оптовая торговля', 'Оптовая база, оптовый склад-магазин', 'store', 6),
    ('rail_freight', 'Ж/д грузовой объект', 'Грузовая станция, грузовой двор, подъездные пути', 'train-front', 7),
    ('port', 'Речной порт', 'Порт, грузовой причал', 'anchor', 8);

-- Объекты и инфраструктура ------------------------------------------------------

CREATE TABLE infra.facility (
    id                  uuid PRIMARY KEY,
    type_code           text NOT NULL REFERENCES ref.facility_type (code),
    name                text,
    status              text NOT NULL DEFAULT 'active',
    point               geometry(Point, 4326) NOT NULL,
    footprint           geometry(MultiPolygon, 4326),
    territory           geometry(MultiPolygon, 4326),
    point_3857          geometry(Point, 3857) GENERATED ALWAYS AS (ST_Transform(point, 3857)) STORED,
    footprint_3857      geometry(MultiPolygon, 3857) GENERATED ALWAYS AS (ST_Transform(footprint, 3857)) STORED,
    building_area_m2    double precision,
    territory_area_m2   double precision,
    buildings           integer NOT NULL DEFAULT 0,
    levels              smallint,
    floor_area_m2       double precision,
    address             text,
    district_id         uuid REFERENCES territory.admin_area (id) ON DELETE SET NULL,
    okrug_id            uuid REFERENCES territory.admin_area (id) ON DELETE SET NULL,
    region_id           uuid REFERENCES territory.admin_area (id) ON DELETE SET NULL,
    ring                text,
    confidence          real NOT NULL,
    confidence_reasons  jsonb NOT NULL DEFAULT '[]'::jsonb,
    attrs               jsonb NOT NULL DEFAULT '{}'::jsonb,
    osm_type            char(1) NOT NULL,
    osm_id              bigint NOT NULL,
    source_code         text NOT NULL,
    snapshot_id         uuid,
    first_seen_at       timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT facility_status_chk CHECK (status IN ('active', 'missing', 'excluded')),
    CONSTRAINT facility_ring_chk CHECK (ring IS NULL OR ring IN ('sk', 'ttk', 'mkad', 'ckad', 'outside')),
    CONSTRAINT facility_confidence_chk CHECK (confidence BETWEEN 0 AND 1),
    CONSTRAINT facility_osm_uq UNIQUE (osm_type, osm_id)
);

COMMENT ON COLUMN infra.facility.floor_area_m2 IS 'оценка: площадь застройки × этажность (модель)';
COMMENT ON COLUMN infra.facility.ring IS 'внутреннее из колец, внутри которого объект; outside — за ЦКАД';

CREATE INDEX facility_point_idx ON infra.facility USING gist (point);
CREATE INDEX facility_point_3857_idx ON infra.facility USING gist (point_3857);
CREATE INDEX facility_footprint_3857_idx ON infra.facility USING gist (footprint_3857);
CREATE INDEX facility_type_idx ON infra.facility (type_code) WHERE status = 'active';

CREATE TABLE infra.industrial_zone (
    id            uuid PRIMARY KEY,
    name          text,
    geom          geometry(MultiPolygon, 4326) NOT NULL,
    geom_3857     geometry(MultiPolygon, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    area_m2       double precision NOT NULL,
    tags          jsonb NOT NULL DEFAULT '{}'::jsonb,
    osm_type      char(1) NOT NULL,
    osm_id        bigint NOT NULL,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT industrial_zone_osm_uq UNIQUE (osm_type, osm_id)
);

CREATE INDEX industrial_zone_geom_idx ON infra.industrial_zone USING gist (geom);
CREATE INDEX industrial_zone_geom_3857_idx ON infra.industrial_zone USING gist (geom_3857);

-- Точечная инфраструктура одной таблицей на вид: общая структура, разные слои карты.
CREATE TABLE infra.poi (
    id            uuid PRIMARY KEY,
    kind          text NOT NULL,
    name          text,
    point         geometry(Point, 4326) NOT NULL,
    point_3857    geometry(Point, 3857) GENERATED ALWAYS AS (ST_Transform(point, 3857)) STORED,
    area          geometry(MultiPolygon, 4326),
    tags          jsonb NOT NULL DEFAULT '{}'::jsonb,
    osm_type      char(1) NOT NULL,
    osm_id        bigint NOT NULL,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT poi_kind_chk CHECK (kind IN (
        'fuel', 'truck_parking', 'rest_area', 'marketplace', 'rail_station', 'rail_freight', 'customs', 'port'
    )),
    CONSTRAINT poi_osm_uq UNIQUE (kind, osm_type, osm_id)
);

COMMENT ON TABLE infra.poi IS
    'АЗС, стоянки грузовиков, площадки отдыха, рынки и ярмарки, вокзалы, ж/д грузовые пункты, таможня, порты';

CREATE INDEX poi_point_3857_idx ON infra.poi USING gist (point_3857);
CREATE INDEX poi_kind_idx ON infra.poi (kind);

-- Дорожная сеть ---------------------------------------------------------------

CREATE TABLE network.road_segment (
    id            uuid PRIMARY KEY,
    osm_id        bigint NOT NULL UNIQUE,
    class         text NOT NULL,
    name          text,
    ref           text,
    oneway        smallint NOT NULL DEFAULT 0,
    lanes         smallint,
    maxspeed      smallint,
    bridge        boolean NOT NULL DEFAULT false,
    tunnel        boolean NOT NULL DEFAULT false,
    layer         smallint NOT NULL DEFAULT 0,
    ring          text,
    length_m      double precision NOT NULL,
    tags          jsonb NOT NULL DEFAULT '{}'::jsonb,
    geom          geometry(LineString, 4326) NOT NULL,
    geom_3857     geometry(LineString, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT road_segment_class_chk CHECK (class IN (
        'motorway', 'trunk', 'primary', 'secondary', 'tertiary', 'unclassified', 'residential',
        'living_street', 'service', 'track'
    )),
    CONSTRAINT road_segment_oneway_chk CHECK (oneway IN (-1, 0, 1))
);

COMMENT ON COLUMN network.road_segment.oneway IS '1 — по направлению линии, -1 — против, 0 — в обе стороны';

CREATE INDEX road_segment_geom_idx ON network.road_segment USING gist (geom);
CREATE INDEX road_segment_geom_3857_idx ON network.road_segment USING gist (geom_3857);
CREATE INDEX road_segment_class_idx ON network.road_segment (class);

-- Ограничение на участке сети.
CREATE TABLE network.restriction (
    id            uuid PRIMARY KEY,
    segment_id    uuid NOT NULL REFERENCES network.road_segment (id) ON DELETE CASCADE,
    kind          text NOT NULL,
    value         numeric,
    unit          text,
    direction     smallint NOT NULL DEFAULT 0,
    condition     text,
    raw           text NOT NULL,
    origin        text NOT NULL,
    confidence    real NOT NULL DEFAULT 1,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    CONSTRAINT restriction_kind_chk CHECK (kind IN (
        'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength',
        'hgv_ban', 'hgv_destination', 'motor_vehicle_ban'
    )),
    CONSTRAINT restriction_origin_chk CHECK (origin IN ('osm', 'sign', 'manual')),
    CONSTRAINT restriction_direction_chk CHECK (direction IN (-1, 0, 1))
);

COMMENT ON COLUMN network.restriction.condition IS 'условие действия (*:conditional), например «Mo-Fr 07:00-22:00»';

CREATE INDEX restriction_segment_idx ON network.restriction (segment_id);
CREATE INDEX restriction_kind_idx ON network.restriction (kind);

CREATE TABLE network.bridge (
    id            uuid PRIMARY KEY,
    segment_id    uuid REFERENCES network.road_segment (id) ON DELETE CASCADE,
    name          text,
    kind          text NOT NULL,
    length_m      double precision NOT NULL,
    maxweight_t   numeric,
    maxheight_m   numeric,
    geom          geometry(LineString, 4326) NOT NULL,
    geom_3857     geometry(LineString, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    osm_id        bigint NOT NULL UNIQUE,
    source_code   text NOT NULL,
    snapshot_id   uuid,
    CONSTRAINT bridge_kind_chk CHECK (kind IN ('bridge', 'viaduct', 'overpass'))
);

CREATE INDEX bridge_geom_3857_idx ON network.bridge USING gist (geom_3857);

-- Разбор величин ограничений: «3.5», «3,5 t», «3500 kg», «4.2 m», «none».
-- +goose StatementBegin
CREATE FUNCTION network.parse_measure(raw text) RETURNS numeric
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN raw IS NULL OR lower(trim(raw)) IN ('none', 'no', 'default', 'unsigned', 'below_default') THEN NULL
        WHEN raw ~* '^\s*[0-9]+([.,][0-9]+)?\s*(t|т)?\s*$'
            THEN replace(substring(raw FROM '[0-9]+([.,][0-9]+)?'), ',', '.')::numeric
        WHEN raw ~* '^\s*[0-9]+([.,][0-9]+)?\s*(m|м)\s*$'
            THEN replace(substring(raw FROM '[0-9]+([.,][0-9]+)?'), ',', '.')::numeric
        WHEN raw ~* '^\s*[0-9]+\s*(kg|кг)\s*$'
            THEN substring(raw FROM '[0-9]+')::numeric / 1000
        ELSE NULL
    END
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION network.parse_measure(text);
DROP TABLE network.bridge;
DROP TABLE network.restriction;
DROP TABLE network.road_segment;
DROP TABLE infra.poi;
DROP TABLE infra.industrial_zone;
DROP TABLE infra.facility;
DROP TABLE ref.facility_type;
DROP TABLE territory.ring_zone;
DROP TABLE territory.admin_area;
DROP SCHEMA osm_stage CASCADE;

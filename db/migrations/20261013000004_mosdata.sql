-- Портал открытых данных Москвы: промежуточная таблица записей наборов, функции разбора,
-- официальные записи точечной инфраструктуры, дорожные знаки и инфраструктура УДС.

-- +goose Up

CREATE SCHEMA IF NOT EXISTS mos_stage;

-- Записи набора из последнего снимка; преобразование набора переносит их в домен.
CREATE UNLOGGED TABLE mos_stage.record (
    dataset   integer NOT NULL,
    global_id bigint NOT NULL,
    cells     jsonb NOT NULL,
    geometry  jsonb
);

CREATE INDEX record_dataset_idx ON mos_stage.record (dataset);

-- Дата из записи портала («дд.мм.гггг», иногда со временем).
CREATE FUNCTION mos_stage.date(v text) RETURNS date
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN CASE WHEN v ~ '^\d{2}\.\d{2}\.\d{4}' THEN to_date(substr(v, 1, 10), 'DD.MM.YYYY') END;

-- Дата и время по Москве («дд.мм.гггг чч:мм»); без времени — начало суток.
CREATE FUNCTION mos_stage.ts(v text) RETURNS timestamptz
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN CASE
        WHEN v ~ '^\d{2}\.\d{2}\.\d{4} \d{1,2}:\d{2}'
            THEN to_timestamp(substring(v FROM '^\d{2}\.\d{2}\.\d{4} \d{1,2}:\d{2}'), 'DD.MM.YYYY HH24:MI')::timestamp AT TIME ZONE 'Europe/Moscow'
        WHEN v ~ '^\d{2}\.\d{2}\.\d{4}'
            THEN to_date(substr(v, 1, 10), 'DD.MM.YYYY')::timestamp AT TIME ZONE 'Europe/Moscow'
    END;

-- Число из атрибута: число JSON или строка с запятой или точкой.
CREATE FUNCTION mos_stage.num(v jsonb) RETURNS numeric
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN CASE
        WHEN jsonb_typeof(v) = 'number' THEN v::text::numeric
        WHEN jsonb_typeof(v) = 'string' AND replace(v #>> '{}', ',', '.') ~ '^\s*-?[0-9]+(\.[0-9]+)?\s*$'
            THEN replace(v #>> '{}', ',', '.')::numeric
    END;

-- Текст атрибута: пустая строка — NULL; значение-список (несколько районов) — через запятую.
CREATE FUNCTION mos_stage.txt(v jsonb) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN CASE
        WHEN v IS NULL OR jsonb_typeof(v) = 'null' THEN NULL
        WHEN jsonb_typeof(v) = 'array' THEN nullif(trim((SELECT string_agg(e #>> '{}', ', ') FROM jsonb_array_elements(v) e)), '')
        ELSE nullif(trim(v #>> '{}'), '')
    END;

-- Геометрия записи (GeoJSON, EPSG:4326); неразбираемая — NULL.
-- +goose StatementBegin
CREATE FUNCTION mos_stage.geom(g jsonb) RETURNS geometry
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
BEGIN
    IF g IS NULL OR jsonb_typeof(g) <> 'object' THEN
        RETURN NULL;
    END IF;
    RETURN ST_MakeValid(ST_SetSRID(ST_GeomFromGeoJSON(g::text), 4326));
EXCEPTION WHEN others THEN
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- Точечная инфраструктура: официальные записи портала рядом с записями OSM ------------

ALTER TABLE infra.poi
    ALTER COLUMN osm_type DROP NOT NULL,
    ALTER COLUMN osm_id DROP NOT NULL,
    ADD COLUMN mos_dataset integer,
    ADD COLUMN mos_global_id bigint,
    ADD COLUMN official_id uuid REFERENCES infra.poi (id) ON DELETE SET NULL,
    ADD COLUMN address text,
    ADD CONSTRAINT poi_origin_chk CHECK (osm_id IS NOT NULL OR mos_global_id IS NOT NULL),
    ADD CONSTRAINT poi_mos_uq UNIQUE (mos_dataset, mos_global_id),
    DROP CONSTRAINT poi_kind_chk,
    ADD CONSTRAINT poi_kind_chk CHECK (kind IN (
        'fuel', 'truck_parking', 'rest_area', 'marketplace', 'fair', 'rail_station', 'rail_freight',
        'customs', 'port', 'tpu', 'technopark'
    ));

COMMENT ON COLUMN infra.poi.official_id IS
    'у записи OSM — совпадающая официальная запись портала Москвы; на карте показывается официальная';

CREATE INDEX poi_official_idx ON infra.poi (official_id) WHERE official_id IS NOT NULL;

-- Дорожные знаки -------------------------------------------------------------------

-- Знаки и таблички (ПДД, приложение 1): названия уточняются по набору знаков портала; вид
-- ограничения для грузового движения — правило системы.
CREATE TABLE ref.road_sign_type (
    code              text PRIMARY KEY,
    name              text NOT NULL,
    sign_group        smallint NOT NULL,
    restriction_kind  text,
    CONSTRAINT road_sign_type_kind_chk CHECK (restriction_kind IS NULL OR restriction_kind IN (
        'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength',
        'hgv_ban', 'motor_vehicle_ban', 'hazmat_ban'
    ))
);

COMMENT ON COLUMN ref.road_sign_type.restriction_kind IS 'вид ограничения для грузового движения, если знак его вводит';

-- Знаки, вводящие ограничения для грузовиков (ПДД, разд. 3 приложения 1).
INSERT INTO ref.road_sign_type (code, name, sign_group, restriction_kind) VALUES
    ('3.2', 'Движение запрещено', 3, 'motor_vehicle_ban'),
    ('3.3', 'Движение механических транспортных средств запрещено', 3, 'motor_vehicle_ban'),
    ('3.4', 'Движение грузовых автомобилей запрещено', 3, 'hgv_ban'),
    ('3.11', 'Ограничение массы', 3, 'maxweight'),
    ('3.12', 'Ограничение массы, приходящейся на ось транспортного средства', 3, 'maxaxleload'),
    ('3.13', 'Ограничение высоты', 3, 'maxheight'),
    ('3.14', 'Ограничение ширины', 3, 'maxwidth'),
    ('3.15', 'Ограничение длины', 3, 'maxlength'),
    ('3.32', 'Движение транспортных средств с опасными грузами запрещено', 3, 'hazmat_ban'),
    ('3.33', 'Движение транспортных средств с взрывчатыми и огнеопасными грузами запрещено', 3, 'hazmat_ban');

CREATE TABLE network.road_sign (
    id              uuid PRIMARY KEY,
    mos_global_id   bigint NOT NULL UNIQUE,
    mos_id          bigint,
    code            text,
    sign_type       text NOT NULL,
    temporary       boolean,
    placed_on       date,
    location        text,
    adm_area        text,
    district        text,
    point           geometry(Point, 4326) NOT NULL,
    point_3857      geometry(Point, 3857) GENERATED ALWAYS AS (ST_Transform(point, 3857)) STORED,
    segment_id      uuid REFERENCES network.road_segment (id) ON DELETE SET NULL,
    match_distance_m real,
    match_status    text NOT NULL DEFAULT 'pending',
    status          text NOT NULL DEFAULT 'active',
    source_code     text NOT NULL,
    snapshot_id     uuid,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT road_sign_status_chk CHECK (status IN ('active', 'missing')),
    CONSTRAINT road_sign_match_chk CHECK (match_status IN ('pending', 'matched', 'ambiguous', 'unmatched'))
);

COMMENT ON COLUMN network.road_sign.code IS 'номер знака по ПДД (3.11, 8.4.1); NULL — не распознан';
COMMENT ON COLUMN network.road_sign.sign_type IS 'тип знака как в наборе («3.11 Ограничение массы»)';

CREATE INDEX road_sign_point_idx ON network.road_sign USING gist (point);
CREATE INDEX road_sign_point_3857_idx ON network.road_sign USING gist (point_3857);
CREATE INDEX road_sign_code_idx ON network.road_sign (code);
CREATE INDEX road_sign_segment_idx ON network.road_sign (segment_id);

-- Работы на дорожных знаках (установка, демонтаж, замена).
CREATE TABLE network.sign_work (
    id             uuid PRIMARY KEY,
    mos_global_id  bigint NOT NULL UNIQUE,
    work_type      text,
    place          text,
    contractor     text,
    work_on        date,
    district       text,
    point          geometry(Point, 4326),
    source_code    text NOT NULL,
    snapshot_id    uuid,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sign_work_point_idx ON network.sign_work USING gist (point);

-- Инфраструктура УДС из реестров Москвы: светофоры, камеры, неровности, выделенные полосы,
-- табло, объекты дорожного хозяйства. Общая структура, вид — в kind, сведения — в attrs.
CREATE TABLE network.road_asset (
    id             uuid PRIMARY KEY,
    kind           text NOT NULL,
    mos_dataset    integer NOT NULL,
    mos_global_id  bigint NOT NULL,
    name           text,
    address        text,
    district       text,
    attrs          jsonb NOT NULL DEFAULT '{}'::jsonb,
    geom           geometry(Geometry, 4326) NOT NULL,
    geom_3857      geometry(Geometry, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    status         text NOT NULL DEFAULT 'active',
    source_code    text NOT NULL,
    snapshot_id    uuid,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT road_asset_kind_chk CHECK (kind IN (
        'traffic_light', 'camera', 'speed_bump', 'dedicated_lane', 'info_board', 'road_object'
    )),
    CONSTRAINT road_asset_status_chk CHECK (status IN ('active', 'missing')),
    CONSTRAINT road_asset_mos_uq UNIQUE (mos_dataset, mos_global_id)
);

CREATE INDEX road_asset_kind_idx ON network.road_asset (kind);
CREATE INDEX road_asset_geom_idx ON network.road_asset USING gist (geom);
CREATE INDEX road_asset_geom_3857_idx ON network.road_asset USING gist (geom_3857);

-- Реестр мостов Москвы со ссылкой на совпадающий мост OSM.
CREATE TABLE network.bridge_registry (
    id             uuid PRIMARY KEY,
    mos_global_id  bigint NOT NULL UNIQUE,
    mos_id         bigint,
    name           text NOT NULL,
    location       text,
    crossing       text,
    commissioned   smallint,
    district       text,
    geom           geometry(Geometry, 4326),
    osm_bridge_ids uuid[] NOT NULL DEFAULT '{}',
    source_code    text NOT NULL,
    snapshot_id    uuid,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX bridge_registry_geom_idx ON network.bridge_registry USING gist (geom);

ALTER TABLE network.restriction
    DROP CONSTRAINT restriction_kind_chk,
    ADD CONSTRAINT restriction_kind_chk CHECK (kind IN (
        'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength',
        'hgv_ban', 'hgv_destination', 'motor_vehicle_ban', 'hazmat_ban'
    )),
    ADD COLUMN sign_id uuid REFERENCES network.road_sign (id) ON DELETE CASCADE;

COMMENT ON COLUMN network.restriction.sign_id IS 'знак, которым подтверждено ограничение (origin = sign)';

-- +goose Down

ALTER TABLE network.restriction
    DROP COLUMN sign_id,
    DROP CONSTRAINT restriction_kind_chk,
    ADD CONSTRAINT restriction_kind_chk CHECK (kind IN (
        'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength',
        'hgv_ban', 'hgv_destination', 'motor_vehicle_ban'
    ));
DROP TABLE network.bridge_registry;
DROP TABLE network.road_asset;
DROP TABLE network.sign_work;
DROP TABLE network.road_sign;
DROP TABLE ref.road_sign_type;
DELETE FROM infra.poi WHERE osm_id IS NULL;
ALTER TABLE infra.poi
    DROP CONSTRAINT poi_kind_chk,
    ADD CONSTRAINT poi_kind_chk CHECK (kind IN (
        'fuel', 'truck_parking', 'rest_area', 'marketplace', 'rail_station', 'rail_freight', 'customs', 'port'
    )),
    DROP CONSTRAINT poi_mos_uq,
    DROP CONSTRAINT poi_origin_chk,
    DROP COLUMN address,
    DROP COLUMN official_id,
    DROP COLUMN mos_global_id,
    DROP COLUMN mos_dataset,
    ALTER COLUMN osm_id SET NOT NULL,
    ALTER COLUMN osm_type SET NOT NULL;
DROP SCHEMA mos_stage CASCADE;

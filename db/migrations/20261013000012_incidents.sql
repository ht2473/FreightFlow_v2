-- ДТП с пострадавшими (статистика ГИБДД в обработке «Карты ДТП»): место, время, вид, тяжесть,
-- условия, категории участвовавших ТС. Сведения об участниках (пол, стаж, состояние здоровья,
-- нарушения) и о конкретных ТС (марка, модель, цвет) не хранятся.

-- +goose Up

CREATE TABLE operations.incident (
    id                uuid PRIMARY KEY,
    source_id         bigint NOT NULL UNIQUE,
    occurred_at       timestamptz NOT NULL,
    category          text,
    severity          text NOT NULL,
    dead              smallint NOT NULL DEFAULT 0,
    injured           smallint NOT NULL DEFAULT 0,
    participants      smallint NOT NULL DEFAULT 0,
    light             text,
    weather           text[] NOT NULL DEFAULT '{}',
    road_conditions   text[] NOT NULL DEFAULT '{}',
    nearby            text[] NOT NULL DEFAULT '{}',
    address           text,
    district          text,
    vehicle_categories text[] NOT NULL DEFAULT '{}',
    freight           boolean NOT NULL DEFAULT false,
    heavy_freight     boolean NOT NULL DEFAULT false,
    point             geometry(Point, 4326) NOT NULL,
    point_3857        geometry(Point, 3857) GENERATED ALWAYS AS (ST_Transform(point, 3857)) STORED,
    source_code       text NOT NULL,
    snapshot_id       uuid,
    CONSTRAINT incident_severity_chk CHECK (severity IN ('fatal', 'severe', 'light', 'none'))
);

CREATE INDEX incident_point_idx ON operations.incident USING gist (point_3857);
CREATE INDEX incident_occurred_idx ON operations.incident (occurred_at);

COMMENT ON COLUMN operations.incident.severity IS
    'fatal — с погибшими; severe — тяжёлый вред здоровью; light — лёгкий; none — без пострадавших (нет данных)';
COMMENT ON COLUMN operations.incident.freight IS 'участвовал грузовой автомобиль или грузовой фургон';
COMMENT ON COLUMN operations.incident.heavy_freight IS 'участвовал грузовик, тягач, самосвал, прицеп или спецтехника (без фургонов)';

-- Тайлы ДТП за последние три года и текущий: тяжесть, грузовые ТС, год; подробности — с 14-го
-- масштаба. freight_only — только ДТП с грузовиками.
-- +goose StatementBegin
CREATE FUNCTION tiles.incident_points(z integer, x integer, y integer, layer text, freight_only boolean) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, layer, 4096, 'geom', 'fid'), '')
    FROM (
        SELECT i.source_id AS fid, i.severity, i.freight, i.heavy_freight,
               extract(year FROM i.occurred_at AT TIME ZONE 'Europe/Moscow')::int AS year,
               CASE WHEN z >= 14 THEN i.category END AS category,
               CASE WHEN z >= 14 THEN to_char(i.occurred_at AT TIME ZONE 'Europe/Moscow', 'DD.MM.YYYY HH24:MI') END AS occurred,
               CASE WHEN z >= 14 THEN i.address END AS address,
               CASE WHEN z >= 14 THEN i.dead END AS dead,
               CASE WHEN z >= 14 THEN i.injured END AS injured,
               CASE WHEN z >= 14 THEN array_to_string(i.vehicle_categories, '; ') END AS vehicles,
               ST_AsMVTGeom(i.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM operations.incident i
        WHERE i.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
          AND i.occurred_at >= date_trunc('year', now()) - interval '3 years'
          AND (NOT freight_only OR i.freight)
    ) t
$$;
-- +goose StatementEnd

CREATE FUNCTION tiles.incidents(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.incident_points(z, x, y, 'incidents', false);

CREATE FUNCTION tiles.freight_incidents(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN tiles.incident_points(z, x, y, 'freight_incidents', true);

-- +goose Down

DROP FUNCTION tiles.freight_incidents(integer, integer, integer);
DROP FUNCTION tiles.incidents(integer, integer, integer);
DROP FUNCTION tiles.incident_points(integer, integer, integer, text, boolean);
DROP TABLE operations.incident;

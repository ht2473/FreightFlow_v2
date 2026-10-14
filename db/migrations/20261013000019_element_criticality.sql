-- Критичность мостов и участков: рост времени доступа объектов реестра к МКАД при закрытии
-- элемента — по графу анализа для всех мостов и верхних участков, для верхних элементов — с
-- проверкой матрицами Valhalla.

-- +goose Up

ALTER TABLE network.graph_version ADD COLUMN criticality jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN network.graph_version.criticality IS
    'привязка объектов и точек МКАД к графу, число оценённых и проверенных матрицами элементов';

CREATE TABLE network.element_criticality (
    version_id             integer NOT NULL REFERENCES network.graph_version (id) ON DELETE CASCADE,
    id                     integer NOT NULL,
    kind                   text NOT NULL,
    name                   text NOT NULL,
    segment_ids            uuid[] NOT NULL,
    edge_ids               integer[] NOT NULL,
    centrality             double precision NOT NULL,
    rank                   integer NOT NULL,
    facilities             integer NOT NULL,
    damage_min             real NOT NULL,
    max_delay_min          real NOT NULL,
    disconnected           integer NOT NULL,
    confirmed              boolean NOT NULL DEFAULT false,
    valhalla_facilities    integer,
    valhalla_damage_min    real,
    valhalla_max_delay_min real,
    valhalla_disconnected  integer,
    geom                   geometry(MultiLineString, 4326) NOT NULL,
    geom_3857              geometry(MultiLineString, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    PRIMARY KEY (version_id, id),
    CONSTRAINT element_criticality_kind_chk CHECK (kind IN ('bridge', 'segment'))
);

COMMENT ON TABLE network.element_criticality IS 'мост (проезжие части одного сооружения) или участок дороги (линия OSM)';
COMMENT ON COLUMN network.element_criticality.rank IS
    'место по ущербу (прирост времени плюс 60 мин за объект без доступа): первые 30 по графу — по проверке матрицами, далее по графу';
COMMENT ON COLUMN network.element_criticality.damage_min IS 'суммарный прирост времени до МКАД по объектам, объекто-минуты';
COMMENT ON COLUMN network.element_criticality.disconnected IS 'объектов, теряющих доступ к МКАД';
COMMENT ON COLUMN network.element_criticality.confirmed IS 'проверено матрицами Valhalla с исключением участков элемента';

CREATE INDEX element_criticality_geom_3857_idx ON network.element_criticality USING gist (geom_3857);

-- Тайлы критичности: элементы с ущербом; уровень — по месту в рейтинге.
-- +goose StatementBegin
CREATE FUNCTION tiles.critical_elements(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'critical_elements', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT c.id AS fid, c.name, c.kind, c.rank, c.facilities, round(c.damage_min::numeric)::float8 AS damage_min,
               round(c.max_delay_min::numeric, 1)::float8 AS max_delay_min, c.disconnected, c.confirmed,
               round(c.valhalla_damage_min::numeric)::float8 AS valhalla_damage_min,
               CASE WHEN c.rank <= 10 THEN 'top10' WHEN c.rank <= 50 THEN 'top50' ELSE 'other' END AS level,
               ST_AsMVTGeom(c.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.element_criticality c
        WHERE c.version_id = network.current_graph()
          AND c.geom_3857 && ST_TileEnvelope(z, x, y)
          AND (c.disconnected > 0 OR c.facilities > 0)
          AND (z >= 11 OR c.rank <= 50)
        ORDER BY c.rank DESC
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION tiles.critical_elements(integer, integer, integer);
DROP TABLE network.element_criticality;
ALTER TABLE network.graph_version DROP COLUMN criticality;

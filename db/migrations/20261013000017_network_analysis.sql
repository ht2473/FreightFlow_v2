-- Сетевой анализ: граф участков для профиля ТС, значимость рёбер (центральность) и узкие места
-- по габаритам. Хранятся две последние версии графа.

-- +goose Up

CREATE TABLE network.graph_version (
    id                serial PRIMARY KEY,
    profile_code      text NOT NULL REFERENCES ref.vehicle_profile (code),
    network_version   bigint NOT NULL,
    built_at          timestamptz NOT NULL DEFAULT now(),
    nodes             integer NOT NULL,
    edges             integer NOT NULL,
    arcs              integer NOT NULL,
    largest_component real NOT NULL,
    accuracy          jsonb NOT NULL,
    duration_ms       integer NOT NULL
);

COMMENT ON COLUMN network.graph_version.network_version IS 'версия домена network, по которой построен граф';
COMMENT ON COLUMN network.graph_version.largest_component IS 'доля узлов в наибольшей сильно связной компоненте';
COMMENT ON COLUMN network.graph_version.accuracy IS
    'точность выборочной центральности для профиля (profile) и без пределов габаритов (free): источники, медиана относительной ошибки верхней сотни рёбер, ранговая устойчивость половин выборки';

-- Узлы — перекрёстки и концы линий OSM.
CREATE TABLE network.graph_node (
    version_id integer NOT NULL REFERENCES network.graph_version (id) ON DELETE CASCADE,
    id         integer NOT NULL,
    point      geometry(Point, 4326) NOT NULL,
    PRIMARY KEY (version_id, id)
);

-- Рёбра — части линий между узлами; участок сети — по segment_id (без внешнего ключа: версия
-- графа неизменна после перезагрузки сети).
CREATE TABLE network.graph_edge (
    version_id   integer NOT NULL REFERENCES network.graph_version (id) ON DELETE CASCADE,
    id           integer NOT NULL,
    segment_id   uuid NOT NULL,
    from_node    integer NOT NULL,
    to_node      integer NOT NULL,
    length_m     real NOT NULL,
    minutes      real NOT NULL,
    forward      boolean NOT NULL,
    backward     boolean NOT NULL,
    closed_by    text,
    closed_value real,
    dims_only    boolean NOT NULL DEFAULT false,
    destination  boolean NOT NULL DEFAULT false,
    geom         geometry(LineString, 4326) NOT NULL,
    geom_3857    geometry(LineString, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    PRIMARY KEY (version_id, id)
);

COMMENT ON COLUMN network.graph_edge.minutes IS 'время проезда профилем, мин (без множителя участков «только для подъезда»)';
COMMENT ON COLUMN network.graph_edge.forward IS 'проезд профилю по направлению линии открыт';
COMMENT ON COLUMN network.graph_edge.closed_by IS 'ограничение, закрывающее проезд профилю хотя бы в одном направлении (кроме одностороннего движения)';
COMMENT ON COLUMN network.graph_edge.dims_only IS 'закрыто только пределом массы или габарита';

CREATE INDEX graph_edge_geom_3857_idx ON network.graph_edge USING gist (geom_3857);
CREATE INDEX graph_edge_segment_idx ON network.graph_edge (segment_id);

CREATE TABLE network.edge_metric (
    version_id        integer NOT NULL,
    edge_id           integer NOT NULL,
    centrality        double precision NOT NULL,
    centrality_se     double precision NOT NULL,
    rank              integer NOT NULL,
    percentile        real NOT NULL,
    free_centrality   double precision NOT NULL,
    free_rank         integer NOT NULL,
    free_percentile   real NOT NULL,
    bottleneck        boolean NOT NULL DEFAULT false,
    detour_min        real,
    PRIMARY KEY (version_id, edge_id),
    FOREIGN KEY (version_id, edge_id) REFERENCES network.graph_edge (version_id, id) ON DELETE CASCADE
);

COMMENT ON COLUMN network.edge_metric.centrality IS 'доля упорядоченных пар узлов, кратчайший по времени путь между которыми проходит через ребро';
COMMENT ON COLUMN network.edge_metric.percentile IS 'место в рейтинге, доля рёбер (0 — первое)';
COMMENT ON COLUMN network.edge_metric.free_centrality IS 'центральность в графе без пределов массы и габаритов';
COMMENT ON COLUMN network.edge_metric.bottleneck IS 'узкое место: проезд закрыт пределом массы или габарита, без него через ребро шли бы пути';
COMMENT ON COLUMN network.edge_metric.detour_min IS 'дополнительное время объезда узкого места, мин; пусто — объезда нет';

CREATE INDEX edge_metric_rank_idx ON network.edge_metric (version_id, rank);

-- Текущая версия графа.
CREATE FUNCTION network.current_graph() RETURNS integer
    LANGUAGE sql STABLE PARALLEL SAFE
    RETURN (SELECT max(id) FROM network.graph_version);

-- Тайлы значимости: верхние 20 % рёбер (на мелких масштабах — 5 %), уровень — по месту в рейтинге.
-- +goose StatementBegin
CREATE FUNCTION tiles.edge_centrality(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'edge_centrality', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT e.id AS fid, coalesce(s.name, s.ref) AS name, m.rank,
               round((m.centrality * 100)::numeric, 3)::float8 AS share,
               CASE WHEN m.percentile <= 0.01 THEN 'top1' WHEN m.percentile <= 0.05 THEN 'top5' ELSE 'top20' END AS level,
               ST_AsMVTGeom(e.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.graph_edge e
        JOIN network.edge_metric m ON m.version_id = e.version_id AND m.edge_id = e.id
        LEFT JOIN network.road_segment s ON s.id = e.segment_id
        WHERE e.version_id = network.current_graph()
          AND e.geom_3857 && ST_TileEnvelope(z, x, y)
          AND m.centrality > 0
          AND m.percentile <= CASE WHEN z < 11 THEN 0.05 ELSE 0.2 END
        ORDER BY m.rank DESC
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Тайлы узких мест: рёбра, закрытые профилю пределом массы или габарита, с важностью пути по
-- месту в рейтинге без ограничений.
-- +goose StatementBegin
CREATE FUNCTION tiles.bottlenecks(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'bottlenecks', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT e.id AS fid, coalesce(s.name, s.ref) AS name, e.closed_by AS kind, e.closed_value::float8 AS value,
               m.free_rank, round(m.detour_min::numeric, 1)::float8 AS detour_min, m.detour_min IS NULL AS no_detour,
               CASE WHEN m.free_percentile <= 0.05 THEN 'high' WHEN m.free_percentile <= 0.2 THEN 'medium' ELSE 'low' END AS importance,
               ST_AsMVTGeom(e.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM network.graph_edge e
        JOIN network.edge_metric m ON m.version_id = e.version_id AND m.edge_id = e.id
        LEFT JOIN network.road_segment s ON s.id = e.segment_id
        WHERE e.version_id = network.current_graph()
          AND e.geom_3857 && ST_TileEnvelope(z, x, y)
          AND m.bottleneck
        ORDER BY m.free_rank DESC
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION tiles.bottlenecks(integer, integer, integer);
DROP FUNCTION tiles.edge_centrality(integer, integer, integer);
DROP FUNCTION network.current_graph();
DROP TABLE network.edge_metric;
DROP TABLE network.graph_edge;
DROP TABLE network.graph_node;
DROP TABLE network.graph_version;

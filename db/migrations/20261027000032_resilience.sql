-- +goose Up

-- Анализ устойчивости: пакет сценариев выпадения верхних элементов рейтинга критичности (мосты и
-- участки) с полным расчётом доступности объектов по нескольким видам целей.
CREATE TABLE scenario.resilience_run (
    id             serial PRIMARY KEY,
    graph_version  integer NOT NULL,
    graph_built_at timestamptz NOT NULL,
    profile_code   text NOT NULL,
    targets        text[] NOT NULL,
    radius_km      real NOT NULL,
    status         text NOT NULL DEFAULT 'running',
    elements       integer NOT NULL DEFAULT 0,
    requests       integer NOT NULL DEFAULT 0,
    error          text,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    CONSTRAINT resilience_run_status_chk CHECK (status IN ('running', 'done', 'failed'))
);

COMMENT ON COLUMN scenario.resilience_run.graph_version IS 'версия сетевого анализа (network.element_criticality.version_id)';
COMMENT ON COLUMN scenario.resilience_run.radius_km IS 'объекты в этом радиусе от элемента пересчитываются, дальние считаются незатронутыми';

-- Ущерб от выпадения элемента: по целям и итог. damage — Σ прироста времени (объекто-минуты) и
-- 60 минут за каждый объект, потерявший доступ, по всем видам целей.
CREATE TABLE scenario.resilience (
    run_id           integer NOT NULL REFERENCES scenario.resilience_run (id) ON DELETE CASCADE,
    element_id       integer NOT NULL,
    kind             text NOT NULL,
    name             text NOT NULL,
    criticality_rank integer NOT NULL,
    segment_ids      uuid[] NOT NULL,
    candidates       integer NOT NULL,
    affected         integer NOT NULL,
    lost             integer NOT NULL,
    delay_min        real NOT NULL,
    max_delay_min    real NOT NULL,
    damage           real NOT NULL,
    rank             integer NOT NULL,
    by_target        jsonb NOT NULL,
    geom             geometry(MultiLineString, 4326) NOT NULL,
    geom_3857        geometry(MultiLineString, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    PRIMARY KEY (run_id, element_id)
);

COMMENT ON COLUMN scenario.resilience.by_target IS 'по виду цели: затронуто, потеряли доступ, прирост и наибольший прирост, ущерб';

CREATE INDEX resilience_geom_3857_idx ON scenario.resilience USING gist (geom_3857);

-- Последний завершённый пакет.
-- +goose StatementBegin
CREATE FUNCTION scenario.current_resilience() RETURNS integer
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT max(id) FROM scenario.resilience_run WHERE status = 'done'
$$;
-- +goose StatementEnd

-- Тайлы устойчивости: элементы последнего пакета; уровень — по месту в рейтинге ущерба.
-- +goose StatementBegin
CREATE FUNCTION tiles.resilience(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'resilience', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT r.element_id AS fid, r.name, r.kind, r.rank, r.criticality_rank, r.affected, r.lost,
               round(r.damage::numeric)::float8 AS damage, round(r.delay_min::numeric)::float8 AS delay_min,
               round(r.max_delay_min::numeric, 1)::float8 AS max_delay_min,
               CASE WHEN r.rank <= 10 THEN 'top10' WHEN r.damage > 0 THEN 'other' ELSE 'none' END AS level,
               ST_AsMVTGeom(r.geom_3857, ST_TileEnvelope(z, x, y), 4096, 16, true) AS geom
        FROM scenario.resilience r
        WHERE r.run_id = scenario.current_resilience()
          AND r.geom_3857 && ST_TileEnvelope(z, x, y)
        ORDER BY r.rank DESC
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION tiles.resilience(integer, integer, integer);
DROP FUNCTION scenario.current_resilience();
DROP TABLE scenario.resilience;
DROP TABLE scenario.resilience_run;

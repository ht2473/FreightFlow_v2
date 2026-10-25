-- +goose Up

-- Ячейки H3 в границах Москвы: разрешение 8 (≈ 0,74 км²) — город, 9 (≈ 0,11 км²) — детализация.
-- Ячейка принадлежит району, где лежит её центр.
CREATE TABLE territory.h3_cell (
    h3          h3index PRIMARY KEY,
    res         smallint NOT NULL CHECK (res IN (8, 9)),
    district_id uuid REFERENCES territory.admin_area (id) ON DELETE SET NULL,
    okrug_id    uuid REFERENCES territory.admin_area (id) ON DELETE SET NULL,
    geom        geometry(Polygon, 4326) NOT NULL,
    geom_3857   geometry(Polygon, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    area_km2    double precision NOT NULL
);

CREATE INDEX h3_cell_res_idx ON territory.h3_cell (res);
CREATE INDEX h3_cell_geom_idx ON territory.h3_cell USING gist (geom);
CREATE INDEX h3_cell_geom_3857_idx ON territory.h3_cell USING gist (geom_3857);

-- Подготовительные шаги расчёта показателей (ячейки H3): подпись данных, по которым построены.
CREATE TABLE analytics.prepare_state (
    step        text PRIMARY KEY,
    signature   text NOT NULL,
    rows        integer NOT NULL,
    prepared_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE analytics.prepare_state;
DROP TABLE territory.h3_cell;

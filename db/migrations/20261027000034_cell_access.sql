-- +goose Up

-- Доступность ячеек H3 разрешения 8: время грузовика 12 т от центра ячейки до ближайшей цели вида
-- (МКАД, ТТК, ж/д грузовые пункты) по графу маршрутизации. Критерий оценки локации: в отличие от
-- доступности объектов, значение есть у каждой ячейки.
CREATE TABLE analytics.cell_access (
    h3             h3index NOT NULL,
    target_kind    text NOT NULL,
    minutes        real,
    km             real,
    graph_built_at timestamptz NOT NULL,
    PRIMARY KEY (h3, target_kind)
);

COMMENT ON COLUMN analytics.cell_access.minutes IS 'время до ближайшей цели, мин; пусто — центр ячейки не привязан к сети или путь не найден';

-- +goose Down
DROP TABLE analytics.cell_access;

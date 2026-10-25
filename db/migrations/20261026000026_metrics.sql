-- +goose Up

-- Каталог показателей — выгрузка описаний из кода (internal/analytics) при каждом пересчёте.
CREATE TABLE analytics.metric_def (
    code       text PRIMARY KEY,
    title      text NOT NULL,
    unit       text NOT NULL,
    levels     text[] NOT NULL,
    formula    text NOT NULL,
    method     text NOT NULL,
    domains    text[] NOT NULL,
    depends    text[] NOT NULL,
    version    integer NOT NULL,
    synced_at  timestamptz NOT NULL DEFAULT now()
);

-- Значения показателей. entity — объект уровня (id объекта, территории, участка, индекс ячейки
-- H3, «moscow» для города); period — период ряда (без периода — текущее значение).
CREATE TABLE analytics.metric_value (
    metric_code text NOT NULL REFERENCES analytics.metric_def (code) ON DELETE CASCADE,
    level       text NOT NULL CHECK (level IN ('facility', 'district', 'okrug', 'segment', 'city', 'h3_8', 'h3_9')),
    entity      text NOT NULL,
    period      date,
    value       double precision NOT NULL,
    -- Входы расчёта: слагаемые формулы, число наблюдений.
    inputs      jsonb NOT NULL DEFAULT '{}',
    computed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT metric_value_key UNIQUE NULLS NOT DISTINCT (metric_code, level, entity, period)
);

CREATE INDEX metric_value_entity_idx ON analytics.metric_value (level, entity);

-- Состояние расчёта: подпись входных данных, по которым посчитаны значения.
CREATE TABLE analytics.metric_state (
    code        text PRIMARY KEY REFERENCES analytics.metric_def (code) ON DELETE CASCADE,
    signature   text NOT NULL,
    rows        integer NOT NULL,
    duration_ms integer NOT NULL,
    computed_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE analytics.metric_state;
DROP TABLE analytics.metric_value;
DROP TABLE analytics.metric_def;

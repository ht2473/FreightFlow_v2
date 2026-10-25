-- +goose Up

-- Настройки сводных индексов: веса составляющих и нормировка. Без владельца — по умолчанию
-- (задаёт администратор), с владельцем — свои у пользователя.
CREATE TABLE analytics.index_config (
    index_code    text NOT NULL,
    owner_id      uuid REFERENCES core.app_user (id) ON DELETE CASCADE,
    weights       jsonb NOT NULL,
    normalization text NOT NULL CHECK (normalization IN ('minmax', 'zscore')),
    updated_by    uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT index_config_key UNIQUE NULLS NOT DISTINCT (index_code, owner_id)
);

-- +goose Down
DROP TABLE analytics.index_config;

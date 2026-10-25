-- +goose Up

-- Виджеты главного дашборда пользователя (без строки — набор по умолчанию).
CREATE TABLE analytics.dashboard (
    owner_id   uuid PRIMARY KEY REFERENCES core.app_user (id) ON DELETE CASCADE,
    widgets    jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE analytics.dashboard;

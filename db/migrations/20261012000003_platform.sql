-- Платформа: очередь фоновых задач и исходящие события.

-- +goose Up

-- Таблицы очереди River создаёт мигратор River в этой схеме (platform/migrate).
CREATE SCHEMA IF NOT EXISTS queue;

-- Доменные события пишутся в одной транзакции с изменением; после фиксации
-- NOTIFY сообщает слушателям номер записи (platform/events).
CREATE TABLE notify.outbox (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type        text NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbox_type_chk CHECK (type ~ '^[a-z]+(\.[a-z_]+)+$')
);

CREATE INDEX outbox_created_at_idx ON notify.outbox (created_at);

-- +goose Down

DROP TABLE notify.outbox;
DROP SCHEMA queue CASCADE;

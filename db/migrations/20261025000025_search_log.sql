-- +goose Up

-- Журнал поисковых запросов: качество поиска и частые запросы. Хранится 90 дней.
CREATE TABLE search.query_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     uuid,
    mode        text NOT NULL CHECK (mode IN ('search', 'suggest')),
    query       text NOT NULL,
    filters     jsonb NOT NULL DEFAULT '[]',
    results     integer NOT NULL,
    duration_ms integer NOT NULL,
    at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX query_log_at_idx ON search.query_log (at);

-- +goose Down
DROP TABLE search.query_log;

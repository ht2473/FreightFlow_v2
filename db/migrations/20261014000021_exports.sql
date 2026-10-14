-- Выгрузки данных заданиями: параметры выборки, формат, состояние, файл в хранилище. Файлы
-- хранятся неделю.

-- +goose Up

CREATE TABLE report.export (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    kind        text NOT NULL,
    format      text NOT NULL,
    params      jsonb NOT NULL DEFAULT '{}'::jsonb,
    status      text NOT NULL DEFAULT 'queued',
    rows        integer,
    file_path   text,
    file_size   bigint,
    error       text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    expires_at  timestamptz NOT NULL DEFAULT now() + interval '7 days',
    CONSTRAINT export_kind_chk CHECK (kind IN ('facilities')),
    CONSTRAINT export_format_chk CHECK (format IN ('csv', 'xlsx', 'geojson')),
    CONSTRAINT export_status_chk CHECK (status IN ('queued', 'running', 'done', 'failed'))
);

COMMENT ON COLUMN report.export.params IS 'фильтр и сортировка выборки на момент запроса';
COMMENT ON COLUMN report.export.file_path IS 'путь файла в хранилище (blob)';

CREATE INDEX export_user_idx ON report.export (user_id, created_at DESC);
CREATE INDEX export_expires_idx ON report.export (expires_at);

-- +goose Down

DROP TABLE report.export;

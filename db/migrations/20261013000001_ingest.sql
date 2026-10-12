-- Каркас загрузок: реестр источников, запуски, снимки, проверки, версии опубликованных данных.

-- +goose Up

-- Зеркало описаний источников из кода и их настройки.
CREATE TABLE ingest.source (
    code         text PRIMARY KEY,
    title        text NOT NULL,
    publisher    text NOT NULL,
    url          text NOT NULL,
    license      text NOT NULL,
    attribution  text NOT NULL,
    domains      text[] NOT NULL,
    interval_s   integer,
    ru_network   boolean NOT NULL DEFAULT false,
    enabled      boolean NOT NULL DEFAULT true,
    registered   boolean NOT NULL DEFAULT true,
    synced_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT source_code_chk CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT source_interval_chk CHECK (interval_s IS NULL OR interval_s >= 60)
);

COMMENT ON COLUMN ingest.source.registered IS 'источник описан в текущей версии кода';
COMMENT ON COLUMN ingest.source.interval_s IS 'период автоматической загрузки; NULL — только вручную';

-- Сырой снимок источника в хранилище файлов.
CREATE TABLE ingest.snapshot (
    id              uuid PRIMARY KEY,
    source_code     text NOT NULL REFERENCES ingest.source (code),
    path            text NOT NULL,
    sha256          text NOT NULL,
    size_bytes      bigint NOT NULL,
    format          text NOT NULL,
    source_version  text,
    record_count    bigint,
    validators      jsonb NOT NULL DEFAULT '{}'::jsonb,
    fetched_at      timestamptz NOT NULL DEFAULT now(),
    loaded_at       timestamptz,
    CONSTRAINT snapshot_source_sha_uq UNIQUE (source_code, sha256)
);

CREATE INDEX snapshot_source_loaded_idx ON ingest.snapshot (source_code, loaded_at DESC NULLS LAST);

-- Запуск загрузки.
CREATE TABLE ingest.run (
    id           uuid PRIMARY KEY,
    source_code  text NOT NULL REFERENCES ingest.source (code),
    status       text NOT NULL,
    trigger      text NOT NULL,
    forced       boolean NOT NULL DEFAULT false,
    started_by   uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    queued_at    timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    finished_at  timestamptz,
    snapshot_id  uuid REFERENCES ingest.snapshot (id) ON DELETE SET NULL,
    stats        jsonb NOT NULL DEFAULT '{}'::jsonb,
    error        text,
    log          jsonb NOT NULL DEFAULT '[]'::jsonb,
    CONSTRAINT run_status_chk CHECK (status IN ('queued', 'running', 'succeeded', 'unchanged', 'failed')),
    CONSTRAINT run_trigger_chk CHECK (trigger IN ('schedule', 'manual', 'cli'))
);

CREATE INDEX run_source_queued_idx ON ingest.run (source_code, queued_at DESC);

-- Не больше одного незавершённого запуска на источник.
CREATE UNIQUE INDEX run_active_uq ON ingest.run (source_code) WHERE status IN ('queued', 'running');

-- Схема набора на момент снимка: изменения схемы — повод проверить загрузчик.
CREATE TABLE ingest.schema_snapshot (
    id           uuid PRIMARY KEY,
    source_code  text NOT NULL REFERENCES ingest.source (code),
    snapshot_id  uuid REFERENCES ingest.snapshot (id) ON DELETE SET NULL,
    schema       jsonb NOT NULL,
    hash         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX schema_snapshot_source_idx ON ingest.schema_snapshot (source_code, created_at DESC);

-- Результаты проверок качества запуска.
CREATE TABLE ingest.quality_check (
    id          uuid PRIMARY KEY,
    run_id      uuid NOT NULL REFERENCES ingest.run (id) ON DELETE CASCADE,
    code        text NOT NULL,
    title       text NOT NULL,
    severity    text NOT NULL,
    passed      boolean NOT NULL,
    observed    jsonb,
    expected    jsonb,
    message     text,
    checked_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quality_check_severity_chk CHECK (severity IN ('info', 'warning', 'critical'))
);

CREATE INDEX quality_check_run_idx ON ingest.quality_check (run_id);

-- Версия опубликованных данных домена: ключ кешей и ETag.
CREATE TABLE ingest.data_version (
    domain        text PRIMARY KEY,
    version       bigint NOT NULL,
    published_at  timestamptz NOT NULL DEFAULT now(),
    source_code   text REFERENCES ingest.source (code),
    run_id        uuid REFERENCES ingest.run (id) ON DELETE SET NULL
);

-- +goose Down

DROP TABLE ingest.data_version;
DROP TABLE ingest.quality_check;
DROP TABLE ingest.schema_snapshot;
DROP TABLE ingest.run;
DROP TABLE ingest.snapshot;
DROP TABLE ingest.source;

-- +goose Up

-- Расписание отчёта: шаблон и параметры, периодичность (час — по Москве), получатели письма
-- с файлом. next_run_at — когда сформировать следующий отчёт.
CREATE TABLE report.schedule (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    template    text NOT NULL,
    format      text NOT NULL,
    params      jsonb NOT NULL DEFAULT '{}'::jsonb,
    title       text NOT NULL,
    frequency   text NOT NULL,
    weekday     smallint,
    month_day   smallint,
    hour        smallint NOT NULL DEFAULT 8,
    recipients  text[] NOT NULL DEFAULT '{}',
    enabled     boolean NOT NULL DEFAULT true,
    next_run_at timestamptz NOT NULL,
    last_run_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schedule_format_chk CHECK (format IN ('pdf', 'xlsx')),
    CONSTRAINT schedule_frequency_chk CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    CONSTRAINT schedule_weekday_chk CHECK (weekday IS NULL OR weekday BETWEEN 1 AND 7),
    CONSTRAINT schedule_day_chk CHECK (month_day IS NULL OR month_day BETWEEN 1 AND 28),
    CONSTRAINT schedule_hour_chk CHECK (hour BETWEEN 0 AND 23),
    CONSTRAINT schedule_recipients_chk CHECK (cardinality(recipients) <= 10),
    CONSTRAINT schedule_title_chk CHECK (length(title) BETWEEN 1 AND 200)
);

CREATE INDEX schedule_due_idx ON report.schedule (next_run_at) WHERE enabled;

-- Отчёт: шаблон, параметры, формат, состояние и файл в хранилище; хранится неделю.
CREATE TABLE report.report (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    template    text NOT NULL,
    format      text NOT NULL,
    params      jsonb NOT NULL DEFAULT '{}'::jsonb,
    title       text NOT NULL,
    status      text NOT NULL DEFAULT 'queued',
    file_path   text,
    file_size   bigint,
    pages       integer,
    error       text,
    schedule_id uuid REFERENCES report.schedule (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    expires_at  timestamptz NOT NULL DEFAULT now() + interval '7 days',
    CONSTRAINT report_format_chk CHECK (format IN ('pdf', 'xlsx')),
    CONSTRAINT report_status_chk CHECK (status IN ('queued', 'running', 'done', 'failed'))
);

CREATE INDEX report_user_idx ON report.report (user_id, created_at DESC);
CREATE INDEX report_expires_idx ON report.report (expires_at);

ALTER TABLE notify.notification DROP CONSTRAINT notification_kind_chk;
ALTER TABLE notify.notification ADD CONSTRAINT notification_kind_chk
    CHECK (kind IN ('alert_created', 'alert_assigned', 'report_ready', 'report_failed', 'test'));

-- +goose Down

DELETE FROM notify.notification WHERE kind IN ('report_ready', 'report_failed');
ALTER TABLE notify.notification DROP CONSTRAINT notification_kind_chk;
ALTER TABLE notify.notification ADD CONSTRAINT notification_kind_chk
    CHECK (kind IN ('alert_created', 'alert_assigned', 'test'));
DROP TABLE report.report;
DROP TABLE report.schedule;

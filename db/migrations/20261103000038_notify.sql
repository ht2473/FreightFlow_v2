-- +goose Up

-- Уведомление пользователю в системе (центр уведомлений).
CREATE TABLE notify.notification (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    severity   text NOT NULL DEFAULT 'info',
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    link       text,
    alert_id   uuid REFERENCES alerting.alert (id) ON DELETE CASCADE,
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_kind_chk CHECK (kind IN ('alert_created', 'alert_assigned', 'test')),
    CONSTRAINT notification_severity_chk CHECK (severity IN ('info', 'warning', 'critical'))
);

CREATE INDEX notification_user_idx ON notify.notification (user_id, created_at DESC, id DESC);
CREATE INDEX notification_unread_idx ON notify.notification (user_id) WHERE read_at IS NULL;

-- Предпочтения доставки: почта сразу, дайджестом или нет; тихие часы (по Москве) откладывают
-- некритичные почту и Telegram до своего конца; оператор может подписаться на все алерты.
CREATE TABLE notify.preference (
    user_id            uuid PRIMARY KEY REFERENCES core.app_user (id) ON DELETE CASCADE,
    email_mode         text NOT NULL DEFAULT 'instant',
    digest_hour        smallint NOT NULL DEFAULT 9,
    quiet_from         smallint,
    quiet_to           smallint,
    min_severity       text NOT NULL DEFAULT 'info',
    subscribe_all      boolean NOT NULL DEFAULT false,
    subscribe_severity text NOT NULL DEFAULT 'critical',
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT preference_email_chk CHECK (email_mode IN ('instant', 'digest', 'off')),
    CONSTRAINT preference_digest_chk CHECK (digest_hour BETWEEN 0 AND 23),
    CONSTRAINT preference_quiet_chk CHECK ((quiet_from IS NULL) = (quiet_to IS NULL)
        AND (quiet_from IS NULL OR quiet_from BETWEEN 0 AND 23 AND quiet_to BETWEEN 0 AND 23 AND quiet_from <> quiet_to)),
    CONSTRAINT preference_min_chk CHECK (min_severity IN ('info', 'warning', 'critical')),
    CONSTRAINT preference_sub_chk CHECK (subscribe_severity IN ('info', 'warning', 'critical'))
);

-- Привязка Telegram: чат пользователя и одноразовые коды привязки (хранится хеш кода).
CREATE TABLE notify.telegram_binding (
    user_id  uuid PRIMARY KEY REFERENCES core.app_user (id) ON DELETE CASCADE,
    chat_id  bigint NOT NULL UNIQUE,
    username text,
    bound_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notify.telegram_code (
    code_hash  text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

-- Позиция чтения обновлений бота (getUpdates offset).
CREATE TABLE notify.telegram_state (
    id     boolean PRIMARY KEY DEFAULT true CHECK (id),
    offset_id bigint NOT NULL DEFAULT 0
);

-- Вебхук пользователя: адрес HTTPS и секрет подписи (зашифрован ключом приложения).
CREATE TABLE notify.webhook (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    name       text NOT NULL,
    url        text NOT NULL,
    secret     text NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT webhook_name_chk CHECK (length(name) BETWEEN 1 AND 100),
    CONSTRAINT webhook_url_chk CHECK (length(url) BETWEEN 8 AND 2000)
);

CREATE INDEX webhook_user_idx ON notify.webhook (user_id);

-- Доставка уведомления по внешнему каналу: цель, статус, попытки, ответ.
CREATE TABLE notify.delivery (
    id              uuid PRIMARY KEY,
    notification_id uuid REFERENCES notify.notification (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    channel         text NOT NULL,
    webhook_id      uuid REFERENCES notify.webhook (id) ON DELETE CASCADE,
    event           text NOT NULL,
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL DEFAULT 'pending',
    attempts        integer NOT NULL DEFAULT 0,
    response_code   integer,
    last_error      text,
    scheduled_at    timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT delivery_channel_chk CHECK (channel IN ('email', 'telegram', 'webhook')),
    CONSTRAINT delivery_status_chk CHECK (status IN ('pending', 'digest', 'sent', 'failed', 'skipped')),
    CONSTRAINT delivery_webhook_chk CHECK ((channel = 'webhook') = (webhook_id IS NOT NULL))
);

CREATE INDEX delivery_user_idx ON notify.delivery (user_id, created_at DESC);
CREATE INDEX delivery_webhook_idx ON notify.delivery (webhook_id, created_at DESC) WHERE webhook_id IS NOT NULL;
CREATE INDEX delivery_digest_idx ON notify.delivery (user_id) WHERE status = 'digest';

-- +goose Down

DROP TABLE notify.delivery;
DROP TABLE notify.webhook;
DROP TABLE notify.telegram_state;
DROP TABLE notify.telegram_code;
DROP TABLE notify.telegram_binding;
DROP TABLE notify.preference;
DROP TABLE notify.notification;

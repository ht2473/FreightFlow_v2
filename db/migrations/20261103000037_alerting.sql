-- +goose Up

-- Правило оповещения пользователя. Область (area) вычисляется при сохранении из того, как она
-- задана (scope): линия коридора с буфером, объекты с радиусом или многоугольник. state — прошлое
-- состояние для видов, которые сравнивают данные с предыдущей проверкой (ограничения, доступность).
CREATE TABLE alerting.rule (
    id          uuid PRIMARY KEY,
    owner_id    uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    name        text NOT NULL,
    kind        text NOT NULL,
    scope       jsonb NOT NULL DEFAULT '{}'::jsonb,
    area        geometry(MultiPolygon, 4326),
    params      jsonb NOT NULL DEFAULT '{}'::jsonb,
    severity    text NOT NULL DEFAULT 'warning',
    channels    text[] NOT NULL DEFAULT '{in_app}',
    enabled     boolean NOT NULL DEFAULT true,
    state       jsonb,
    checked_at  timestamptz,
    check_error text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rule_name_chk CHECK (length(name) BETWEEN 1 AND 200),
    CONSTRAINT rule_kind_chk CHECK (kind IN ('roadworks', 'restrictions', 'accessibility', 'facilities', 'sources')),
    CONSTRAINT rule_severity_chk CHECK (severity IN ('info', 'warning', 'critical')),
    CONSTRAINT rule_channels_chk CHECK (channels <@ ARRAY['in_app', 'email', 'telegram', 'webhook'])
);

CREATE INDEX rule_owner_idx ON alerting.rule (owner_id);
CREATE INDEX rule_area_idx ON alerting.rule USING gist (area);

-- Алерт: что затронуто (entity — «road_work:<id>», «facility:<id>», «source:<код>»), важность,
-- статус. Пока алерт не закрыт, повторные срабатывания правила по той же сущности добавляются
-- к нему (occurrences), а не создают новый.
CREATE TABLE alerting.alert (
    id              uuid PRIMARY KEY,
    rule_id         uuid NOT NULL REFERENCES alerting.rule (id) ON DELETE CASCADE,
    entity          text NOT NULL,
    severity        text NOT NULL,
    status          text NOT NULL DEFAULT 'active',
    title           text NOT NULL,
    summary         text NOT NULL DEFAULT '',
    details         jsonb NOT NULL DEFAULT '{}'::jsonb,
    geom            geometry(Geometry, 4326),
    occurrences     integer NOT NULL DEFAULT 1,
    assignee_id     uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    acknowledged_by uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    acknowledged_at timestamptz,
    closed_by       uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    closed_at       timestamptz,
    resolution      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT alert_severity_chk CHECK (severity IN ('info', 'warning', 'critical')),
    CONSTRAINT alert_status_chk CHECK (status IN ('active', 'acknowledged', 'closed')),
    CONSTRAINT alert_resolution_chk CHECK (resolution IS NULL OR resolution IN ('manual', 'auto')),
    CONSTRAINT alert_closed_chk CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);

CREATE UNIQUE INDEX alert_open_uq ON alerting.alert (rule_id, entity) WHERE status <> 'closed';
CREATE INDEX alert_feed_idx ON alerting.alert (created_at DESC, id DESC);
CREATE INDEX alert_rule_idx ON alerting.alert (rule_id, created_at DESC);
CREATE INDEX alert_status_idx ON alerting.alert (status) WHERE status <> 'closed';

-- Срабатывания правил: ключ изменения (событие работы, новый объект, неудачный запуск)
-- записывается один раз, поэтому повторная проверка того же окна алертов не дублирует.
CREATE TABLE alerting.hit (
    rule_id  uuid NOT NULL REFERENCES alerting.rule (id) ON DELETE CASCADE,
    key      text NOT NULL,
    alert_id uuid REFERENCES alerting.alert (id) ON DELETE SET NULL,
    at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (rule_id, key)
);

CREATE INDEX hit_at_idx ON alerting.hit (at);

-- Журнал алерта: создание, повторы, действия людей и комментарии.
CREATE TABLE alerting.alert_log (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id uuid NOT NULL REFERENCES alerting.alert (id) ON DELETE CASCADE,
    at       timestamptz NOT NULL DEFAULT now(),
    actor_id uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    action   text NOT NULL,
    comment  text,
    details  jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT alert_log_action_chk CHECK (action IN ('created', 'repeated', 'acknowledged', 'closed',
        'reopened', 'assigned', 'commented', 'resolved')),
    CONSTRAINT alert_log_comment_chk CHECK (comment IS NULL OR length(comment) BETWEEN 1 AND 4000)
);

CREATE INDEX alert_log_alert_idx ON alerting.alert_log (alert_id, id);

-- +goose Down

DROP TABLE alerting.alert_log;
DROP TABLE alerting.hit;
DROP TABLE alerting.alert;
DROP TABLE alerting.rule;

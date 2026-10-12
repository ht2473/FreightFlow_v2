-- +goose Up
CREATE TABLE core.role (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    builtin     boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.role_permission (
    role_code  text NOT NULL REFERENCES core.role (code) ON DELETE CASCADE,
    permission text NOT NULL,
    PRIMARY KEY (role_code, permission)
);

CREATE TABLE core.app_user (
    id             uuid PRIMARY KEY,
    email          text NOT NULL,
    name           text NOT NULL,
    password_hash  text,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    status         text NOT NULL DEFAULT 'active',
    totp_secret    bytea,
    totp_enabled   boolean NOT NULL DEFAULT false,
    failed_logins  integer NOT NULL DEFAULT 0,
    locked_until   timestamptz,
    last_login_at  timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_user_status_chk CHECK (status IN ('active', 'blocked')),
    CONSTRAINT app_user_email_chk CHECK (email = lower(email) AND position('@' IN email) > 1)
);
CREATE UNIQUE INDEX app_user_email_key ON core.app_user (email);

CREATE TABLE core.user_role (
    user_id   uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    role_code text NOT NULL REFERENCES core.role (code) ON DELETE RESTRICT,
    PRIMARY KEY (user_id, role_code)
);

CREATE TABLE core.recovery_code (
    user_id   uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    code_hash bytea NOT NULL,
    used_at   timestamptz,
    PRIMARY KEY (user_id, code_hash)
);

-- Хранилище сессий (формат alexedwards/scs pgxstore).
CREATE TABLE core.session (
    token  text PRIMARY KEY,
    data   bytea NOT NULL,
    expiry timestamptz NOT NULL
);
CREATE INDEX session_expiry_idx ON core.session (expiry);

CREATE TABLE core.api_token (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    name         text NOT NULL,
    prefix       text NOT NULL,
    token_hash   bytea NOT NULL,
    scopes       text[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE UNIQUE INDEX api_token_hash_key ON core.api_token (token_hash);
CREATE INDEX api_token_user_idx ON core.api_token (user_id);

-- Одноразовые ссылки: приглашение и сброс пароля. Хранится только хеш токена.
CREATE TABLE core.one_time_token (
    id          uuid PRIMARY KEY,
    purpose     text NOT NULL,
    token_hash  bytea NOT NULL,
    email       text NOT NULL,
    user_id     uuid REFERENCES core.app_user (id) ON DELETE CASCADE,
    role_code   text REFERENCES core.role (code),
    created_by  uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    CONSTRAINT one_time_token_purpose_chk CHECK (purpose IN ('invite', 'password_reset'))
);
CREATE UNIQUE INDEX one_time_token_hash_key ON core.one_time_token (token_hash);

CREATE TABLE core.audit_log (
    id          uuid PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor_id    uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    actor_label text NOT NULL DEFAULT '',
    action      text NOT NULL,
    entity_type text NOT NULL DEFAULT '',
    entity_id   text NOT NULL DEFAULT '',
    ip          inet,
    user_agent  text NOT NULL DEFAULT '',
    details     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_at_idx ON core.audit_log (at DESC);
CREATE INDEX audit_log_actor_idx ON core.audit_log (actor_id, at DESC);
CREATE INDEX audit_log_entity_idx ON core.audit_log (entity_type, entity_id);

CREATE TABLE core.user_setting (
    user_id    uuid PRIMARY KEY REFERENCES core.app_user (id) ON DELETE CASCADE,
    theme      text NOT NULL DEFAULT 'system',
    settings   jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_setting_theme_chk CHECK (theme IN ('light', 'dark', 'system'))
);

CREATE TABLE core.setting (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES core.app_user (id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE core.setting;
DROP TABLE core.user_setting;
DROP TABLE core.audit_log;
DROP TABLE core.one_time_token;
DROP TABLE core.api_token;
DROP TABLE core.session;
DROP TABLE core.recovery_code;
DROP TABLE core.user_role;
DROP TABLE core.app_user;
DROP TABLE core.role_permission;
DROP TABLE core.role;

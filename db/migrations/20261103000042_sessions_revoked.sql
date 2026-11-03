-- +goose Up

-- Время, раньше которого сеансы пользователя недействительны помимо смены пароля: сброс второго
-- фактора администратором, блокировка.
ALTER TABLE core.app_user ADD COLUMN sessions_revoked_at timestamptz;

-- +goose Down

ALTER TABLE core.app_user DROP COLUMN sessions_revoked_at;

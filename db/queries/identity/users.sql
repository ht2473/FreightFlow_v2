-- name: CreateUser :one
INSERT INTO core.app_user (id, email, name, password_hash, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM core.app_user WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM core.app_user WHERE email = $1;

-- name: CountUsers :one
SELECT count(*) FROM core.app_user;

-- name: ListUsers :many
SELECT u.*,
       coalesce(array_agg(ur.role_code ORDER BY ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL),
                '{}')::text[] AS roles
FROM core.app_user u
LEFT JOIN core.user_role ur ON ur.user_id = u.id
GROUP BY u.id
ORDER BY u.name, u.email;

-- name: UpdateUserProfile :one
UPDATE core.app_user SET name = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: UpdateUserStatus :one
UPDATE core.app_user SET status = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: SetPasswordHash :exec
UPDATE core.app_user
SET password_hash = $2, password_changed_at = now(), failed_logins = 0, locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: RehashPassword :exec
UPDATE core.app_user SET password_hash = $2 WHERE id = $1;

-- name: RecordLoginSuccess :exec
UPDATE core.app_user SET failed_logins = 0, locked_until = NULL, last_login_at = now() WHERE id = $1;

-- name: RecordLoginFailure :one
UPDATE core.app_user
SET failed_logins = failed_logins + 1,
    locked_until = CASE WHEN failed_logins + 1 >= @max_attempts::int
                        THEN now() + make_interval(mins => @lock_minutes::int)
                        ELSE locked_until END
WHERE id = @id
RETURNING failed_logins, locked_until;

-- name: SetTOTPSecret :exec
UPDATE core.app_user SET totp_secret = $2, totp_enabled = false, updated_at = now() WHERE id = $1;

-- name: EnableTOTP :exec
UPDATE core.app_user SET totp_enabled = true, updated_at = now() WHERE id = $1 AND totp_secret IS NOT NULL;

-- name: DisableTOTP :exec
UPDATE core.app_user SET totp_secret = NULL, totp_enabled = false, updated_at = now() WHERE id = $1;

-- name: DeleteRecoveryCodes :exec
DELETE FROM core.recovery_code WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO core.recovery_code (user_id, code_hash) VALUES ($1, $2);

-- name: UseRecoveryCode :execrows
UPDATE core.recovery_code SET used_at = now()
WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: CountUnusedRecoveryCodes :one
SELECT count(*) FROM core.recovery_code WHERE user_id = $1 AND used_at IS NULL;

-- name: GetUserSetting :one
SELECT * FROM core.user_setting WHERE user_id = $1;

-- name: UpsertUserSetting :one
INSERT INTO core.user_setting (user_id, theme, settings)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET theme = EXCLUDED.theme, settings = EXCLUDED.settings, updated_at = now()
RETURNING *;

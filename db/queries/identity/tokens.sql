-- name: CreateAPIToken :one
INSERT INTO core.api_token (id, user_id, name, prefix, token_hash, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetActiveAPITokenByHash :one
SELECT t.*, u.status AS user_status
FROM core.api_token t
JOIN core.app_user u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND t.revoked_at IS NULL
  AND (t.expires_at IS NULL OR t.expires_at > now());

-- name: ListAPITokens :many
SELECT * FROM core.api_token WHERE user_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIToken :execrows
UPDATE core.api_token SET revoked_at = now()
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE core.api_token SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: CreateOneTimeToken :one
INSERT INTO core.one_time_token (id, purpose, token_hash, email, user_id, role_code, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetValidOneTimeToken :one
SELECT * FROM core.one_time_token
WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now();

-- name: MarkOneTimeTokenUsed :execrows
UPDATE core.one_time_token SET used_at = now() WHERE id = $1 AND used_at IS NULL;

-- name: ListPendingInvites :many
SELECT * FROM core.one_time_token
WHERE purpose = 'invite' AND used_at IS NULL AND expires_at > now()
ORDER BY created_at DESC;

-- name: DeleteStaleOneTimeTokens :execrows
DELETE FROM core.one_time_token WHERE expires_at < now() - interval '30 days';

-- name: InsertAudit :exec
INSERT INTO core.audit_log (id, actor_id, actor_label, action, entity_type, entity_id, ip, user_agent, details)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListAudit :many
SELECT * FROM core.audit_log
WHERE (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(before_id)::uuid IS NULL OR id < sqlc.narg(before_id))
ORDER BY id DESC
LIMIT @lim;

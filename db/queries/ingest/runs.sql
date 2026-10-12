-- name: CreateRun :one
INSERT INTO ingest.run (id, source_code, status, trigger, forced, started_by)
VALUES ($1, $2, 'queued', $3, $4, $5)
RETURNING *;

-- name: GetRun :one
SELECT * FROM ingest.run WHERE id = $1;

-- name: StartRun :exec
UPDATE ingest.run SET status = 'running', started_at = now(), error = NULL WHERE id = $1;

-- name: AppendRunLog :exec
UPDATE ingest.run
SET log = log || jsonb_build_array(jsonb_build_object('at', now(), 'level', @level::text, 'message', @message::text))
WHERE id = @id;

-- name: FinishRun :exec
UPDATE ingest.run
SET status = $2, finished_at = now(), stats = $3, error = $4, snapshot_id = $5
WHERE id = $1;

-- name: ListRuns :many
SELECT * FROM ingest.run
WHERE source_code = @source_code
  AND (sqlc.narg('before')::timestamptz IS NULL OR queued_at < sqlc.narg('before'))
ORDER BY queued_at DESC
LIMIT @lim;

-- name: FailStaleRuns :execrows
UPDATE ingest.run
SET status = 'failed', finished_at = now(), error = 'прервано: процесс остановился во время загрузки'
WHERE status = 'running' AND started_at < now() - make_interval(secs => @older_than_s::double precision);

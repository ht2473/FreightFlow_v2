-- name: LastLoadedSnapshot :one
SELECT * FROM ingest.snapshot
WHERE source_code = $1 AND loaded_at IS NOT NULL
ORDER BY loaded_at DESC
LIMIT 1;

-- name: SaveSnapshot :one
INSERT INTO ingest.snapshot (id, source_code, path, sha256, size_bytes, format, source_version, record_count, validators)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (source_code, sha256) DO UPDATE SET
    fetched_at = now(), validators = EXCLUDED.validators,
    source_version = coalesce(EXCLUDED.source_version, ingest.snapshot.source_version),
    record_count = coalesce(EXCLUDED.record_count, ingest.snapshot.record_count)
RETURNING *;

-- name: MarkSnapshotLoaded :exec
UPDATE ingest.snapshot SET loaded_at = now(), record_count = coalesce(sqlc.narg('record_count'), record_count)
WHERE id = @id;

-- name: LastSchema :one
SELECT * FROM ingest.schema_snapshot WHERE source_code = $1 ORDER BY created_at DESC LIMIT 1;

-- name: SaveSchema :exec
INSERT INTO ingest.schema_snapshot (id, source_code, snapshot_id, schema, hash) VALUES ($1, $2, $3, $4, $5);

-- name: SaveCheck :exec
INSERT INTO ingest.quality_check (id, run_id, code, title, severity, passed, observed, expected, message)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListChecks :many
SELECT * FROM ingest.quality_check WHERE run_id = $1 ORDER BY checked_at, code;

-- name: BumpDataVersion :one
INSERT INTO ingest.data_version (domain, version, published_at, source_code, run_id)
VALUES ($1, 1, now(), $2, $3)
ON CONFLICT (domain) DO UPDATE SET
    version = ingest.data_version.version + 1, published_at = now(),
    source_code = EXCLUDED.source_code, run_id = EXCLUDED.run_id
RETURNING version;

-- name: ListDataVersions :many
SELECT * FROM ingest.data_version ORDER BY domain;

-- name: FindSnapshotByVersion :one
SELECT * FROM ingest.snapshot
WHERE source_code = $1 AND source_version = $2
ORDER BY fetched_at DESC
LIMIT 1;

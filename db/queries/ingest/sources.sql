-- name: UpsertSource :exec
INSERT INTO ingest.source (code, title, publisher, url, license, attribution, domains, interval_s, ru_network, registered, synced_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, now())
ON CONFLICT (code) DO UPDATE SET
    title = EXCLUDED.title, publisher = EXCLUDED.publisher, url = EXCLUDED.url,
    license = EXCLUDED.license, attribution = EXCLUDED.attribution, domains = EXCLUDED.domains,
    interval_s = EXCLUDED.interval_s, ru_network = EXCLUDED.ru_network,
    registered = true, synced_at = now();

-- name: MarkUnregisteredSources :exec
UPDATE ingest.source SET registered = false WHERE NOT (code = ANY(@codes::text[]));

-- name: GetSource :one
SELECT * FROM ingest.source WHERE code = $1;

-- name: SetSourceEnabled :one
UPDATE ingest.source SET enabled = $2 WHERE code = $1 RETURNING *;

-- name: ListSourcesWithLastRun :many
SELECT s.*,
       (SELECT to_jsonb(r) FROM (
            SELECT id, status, trigger, queued_at, started_at, finished_at, error
            FROM ingest.run WHERE source_code = s.code ORDER BY queued_at DESC LIMIT 1
        ) r) AS last_run,
       (SELECT finished_at FROM ingest.run
        WHERE source_code = s.code AND status IN ('succeeded', 'unchanged')
        ORDER BY finished_at DESC NULLS LAST LIMIT 1) AS last_success_at,
       (SELECT record_count FROM ingest.snapshot
        WHERE source_code = s.code AND loaded_at IS NOT NULL
        ORDER BY loaded_at DESC LIMIT 1) AS record_count
FROM ingest.source s
WHERE s.registered
ORDER BY s.code;

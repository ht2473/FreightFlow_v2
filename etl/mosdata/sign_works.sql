-- Работы на дорожных знаках (набор 62801) → network.sign_work.

INSERT INTO network.sign_work (id, mos_global_id, work_type, place, contractor, work_on, district, point,
                               source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), r.global_id, mos_stage.txt(r.cells->'WorkType'), mos_stage.txt(r.cells->'WorkPlaceDescription'),
       mos_stage.txt(r.cells->'Contractor'), mos_stage.date(r.cells->>'WorkDateTime'), mos_stage.txt(r.cells->'District'),
       (SELECT CASE WHEN GeometryType(g) = 'POINT' THEN g ELSE ST_PointOnSurface(g) END
        FROM mos_stage.geom(r.geometry) g WHERE g IS NOT NULL),
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int
ON CONFLICT (mos_global_id) DO UPDATE SET
    work_type = EXCLUDED.work_type, place = EXCLUDED.place, contractor = EXCLUDED.contractor,
    work_on = EXCLUDED.work_on, district = EXCLUDED.district, point = EXCLUDED.point,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM network.sign_work WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'sign_works' AS key, count(*) AS value FROM network.sign_work
UNION ALL SELECT 'sign_works_new', count(*) FROM network.sign_work WHERE first_seen_at = now();

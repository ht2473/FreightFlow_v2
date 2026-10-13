-- Реестр мостов (набор 2069) → network.bridge_registry; сверка с мостами OSM: участки-мосты
-- в пределах 30 м от геометрии записи реестра.

INSERT INTO network.bridge_registry (id, mos_global_id, mos_id, name, location, crossing, commissioned, district, geom,
                                     source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), r.global_id, mos_stage.num(r.cells->'ID')::bigint, coalesce(mos_stage.txt(r.cells->'Name'), 'Мост'),
       mos_stage.txt(r.cells->'Location'), mos_stage.txt(r.cells->'Crossing'),
       mos_stage.num(r.cells->'YearOfComissioning')::smallint, mos_stage.txt(r.cells->'District'),
       mos_stage.geom(r.geometry), current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int
ON CONFLICT (mos_global_id) DO UPDATE SET
    mos_id = EXCLUDED.mos_id, name = EXCLUDED.name, location = EXCLUDED.location, crossing = EXCLUDED.crossing,
    commissioned = EXCLUDED.commissioned, district = EXCLUDED.district, geom = EXCLUDED.geom,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM network.bridge_registry WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

UPDATE network.bridge_registry r SET osm_bridge_ids = coalesce((
    SELECT array_agg(b.id ORDER BY b.id) FROM network.bridge b
    WHERE r.geom IS NOT NULL AND ST_DWithin(b.geom::geography, r.geom::geography, 30)
), '{}')
WHERE r.last_seen_at = now();

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'bridges_registry' AS key, count(*) AS value FROM network.bridge_registry
UNION ALL SELECT 'bridges_matched_osm', count(*) FROM network.bridge_registry WHERE cardinality(osm_bridge_ids) > 0
UNION ALL SELECT 'bridges_without_geometry', count(*) FROM network.bridge_registry WHERE geom IS NULL;

-- Инфраструктура УДС → network.road_asset: светофоры (62059), камеры фотовидеофиксации (61701),
-- искусственные неровности (674), выделенные полосы (62662), табло (62747), объекты дорожного
-- хозяйства (64032). Вид — по номеру набора; сведения записи — в attrs без служебных полей.

CREATE TEMP TABLE src ON COMMIT DROP AS
SELECT r.global_id,
       CASE current_setting('ff.dataset')::int
           WHEN 62059 THEN 'traffic_light' WHEN 61701 THEN 'camera' WHEN 674 THEN 'speed_bump'
           WHEN 62662 THEN 'dedicated_lane' WHEN 62747 THEN 'info_board' WHEN 64032 THEN 'road_object'
       END AS kind,
       coalesce(mos_stage.txt(r.cells->'Name'), mos_stage.txt(r.cells->'PlaceDescription'),
                mos_stage.txt(r.cells->'Location'), mos_stage.txt(r.cells->'Address')) AS name,
       coalesce(mos_stage.txt(r.cells->'Address'), mos_stage.txt(r.cells->'Location')) AS address,
       mos_stage.txt(r.cells->'District') AS district,
       r.cells - ARRAY['global_id', 'Latitude_WGS84', 'Longitude_WGS84', 'LanePhoto', 'LaneCoordinates',
                       'geoData', 'geodata_center', 'AdmArea', 'District'] AS attrs,
       coalesce(mos_stage.geom(r.geometry),
                ST_SetSRID(ST_MakePoint(mos_stage.num(r.cells->'Longitude_WGS84')::float8,
                                        mos_stage.num(r.cells->'Latitude_WGS84')::float8), 4326)) AS geom
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int;

DELETE FROM src WHERE geom IS NULL OR ST_IsEmpty(geom);

INSERT INTO network.road_asset (id, kind, mos_dataset, mos_global_id, name, address, district, attrs, geom,
                                source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), kind, current_setting('ff.dataset')::int, global_id, name, address, district, attrs, geom,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM src
ON CONFLICT (mos_dataset, mos_global_id) DO UPDATE SET
    name = EXCLUDED.name, address = EXCLUDED.address, district = EXCLUDED.district, attrs = EXCLUDED.attrs,
    geom = EXCLUDED.geom, status = 'active', snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

UPDATE network.road_asset SET status = 'missing'
WHERE mos_dataset = current_setting('ff.dataset')::int AND last_seen_at < now() AND status = 'active';

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'assets_active' AS key, count(*) AS value FROM network.road_asset
WHERE mos_dataset = current_setting('ff.dataset')::int AND status = 'active'
UNION ALL SELECT 'assets_new', count(*) FROM network.road_asset
WHERE mos_dataset = current_setting('ff.dataset')::int AND first_seen_at = now()
UNION ALL SELECT 'assets_missing', count(*) FROM network.road_asset
WHERE mos_dataset = current_setting('ff.dataset')::int AND status = 'missing';

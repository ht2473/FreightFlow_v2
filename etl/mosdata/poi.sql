-- Официальная точечная инфраструктура → infra.poi: стоянки большегрузов (2984), площадки
-- отдыха (61087), АЗС (753, 754), ТПУ (1047, 3290), вокзалы (62201), технопарки (2254),
-- рынки (654), ярмарки (653, 62061, 620). Записи OSM того же вида рядом получают ссылку на
-- официальную запись (official_id): на карте показывается официальная.

CREATE TEMP TABLE src ON COMMIT DROP AS
SELECT r.global_id,
       CASE current_setting('ff.dataset')::int
           WHEN 2984 THEN 'truck_parking' WHEN 61087 THEN 'rest_area' WHEN 753 THEN 'fuel' WHEN 754 THEN 'fuel'
           WHEN 1047 THEN 'tpu' WHEN 3290 THEN 'tpu' WHEN 62201 THEN 'rail_station' WHEN 2254 THEN 'technopark'
           WHEN 654 THEN 'marketplace' ELSE 'fair'
       END AS kind,
       coalesce(mos_stage.txt(r.cells->'Name'), mos_stage.txt(r.cells->'NameStation'),
                mos_stage.txt(r.cells->'ShortName'), mos_stage.txt(r.cells->'FullName'),
                mos_stage.txt(r.cells->'TPUName'),
                CASE current_setting('ff.dataset')::int
                    WHEN 653 THEN 'Региональная ярмарка' || coalesce(' (' || mos_stage.txt(r.cells->'RegionOfFair') || ')', '')
                    WHEN 62061 THEN 'Межрегиональная ярмарка'
                    WHEN 620 THEN 'Ярмарка выходного дня'
                END) AS name,
       coalesce(mos_stage.txt(r.cells->'Address'), mos_stage.txt(r.cells->'Location')) AS address,
       r.cells - ARRAY['global_id', 'Latitude_WGS84', 'Longitude_WGS84', 'Photo', 'SituationalPlan',
                       'INN', 'KPP', 'OGRN', 'PublicPhone'] AS tags,
       g.geom
FROM mos_stage.record r
CROSS JOIN LATERAL (SELECT coalesce(mos_stage.geom(r.geometry),
        ST_SetSRID(ST_MakePoint(mos_stage.num(r.cells->'Longitude_WGS84')::float8,
                                mos_stage.num(r.cells->'Latitude_WGS84')::float8), 4326)) AS geom) g
WHERE r.dataset = current_setting('ff.dataset')::int;

DELETE FROM src WHERE geom IS NULL OR ST_IsEmpty(geom);

INSERT INTO infra.poi (id, kind, name, point, area, tags, address, mos_dataset, mos_global_id, source_code, snapshot_id,
                       first_seen_at, last_seen_at)
SELECT uuidv7(), kind, name,
       CASE WHEN GeometryType(geom) = 'POINT' THEN geom ELSE ST_PointOnSurface(geom) END,
       CASE WHEN ST_Dimension(geom) = 2 THEN ST_Multi(ST_CollectionExtract(geom, 3)) END,
       tags, address, current_setting('ff.dataset')::int, global_id,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM src
ON CONFLICT (mos_dataset, mos_global_id) DO UPDATE SET
    kind = EXCLUDED.kind, name = EXCLUDED.name, point = EXCLUDED.point, area = EXCLUDED.area, tags = EXCLUDED.tags,
    address = EXCLUDED.address, snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM infra.poi WHERE mos_dataset = current_setting('ff.dataset')::int AND last_seen_at < now();

-- Сопоставление с OSM: ближайшая официальная запись того же вида в радиусе вида.
UPDATE infra.poi o SET official_id = NULL
WHERE o.official_id IN (SELECT id FROM infra.poi WHERE mos_dataset = current_setting('ff.dataset')::int);

UPDATE infra.poi o SET official_id = (
    SELECT p.id FROM infra.poi p
    WHERE p.mos_dataset = current_setting('ff.dataset')::int AND p.kind = o.kind
      AND ST_DWithin(p.point::geography, o.point::geography,
                     CASE o.kind WHEN 'fuel' THEN 120 WHEN 'rail_station' THEN 400 WHEN 'marketplace' THEN 300 ELSE 200 END)
    ORDER BY p.point <-> o.point
    LIMIT 1)
WHERE o.osm_id IS NOT NULL AND o.official_id IS NULL AND o.kind IN (SELECT DISTINCT kind FROM src);

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'poi_official' AS key, count(*) AS value FROM infra.poi WHERE mos_dataset = current_setting('ff.dataset')::int
UNION ALL SELECT 'poi_new', count(*) FROM infra.poi
WHERE mos_dataset = current_setting('ff.dataset')::int AND first_seen_at = now()
UNION ALL SELECT 'poi_matched_osm', count(DISTINCT o.official_id) FROM infra.poi o
JOIN infra.poi p ON p.id = o.official_id WHERE p.mos_dataset = current_setting('ff.dataset')::int;

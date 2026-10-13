-- Ограничения для размещения объектов → territory.constraint_zone: зоны охраны (613) и
-- территории (614) объектов культурного наследия, парковые территории (1465).

CREATE TEMP TABLE src ON COMMIT DROP AS
SELECT r.global_id,
       CASE current_setting('ff.dataset')::int WHEN 613 THEN 'heritage_protection' WHEN 614 THEN 'heritage_territory'
                                               ELSE 'park' END AS kind,
       coalesce(mos_stage.txt(r.cells->'CommonName'), mos_stage.txt(r.cells->'ObjectName'),
                mos_stage.txt(r.cells->'ObjectNameOnDoc')) AS name,
       jsonb_strip_nulls(jsonb_build_object(
           'document', mos_stage.txt(r.cells->'ObjectNameOnDoc'), 'location', mos_stage.txt(r.cells->'Location'),
           'protected_natural_area', r.cells->'IsProtectedNaturalArea', 'balance_holder', mos_stage.txt(r.cells->'BalanceholderComp')
       )) AS attrs,
       ST_Multi(ST_CollectionExtract(mos_stage.geom(r.geometry), 3)) AS geom
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int;

DELETE FROM src WHERE geom IS NULL OR ST_IsEmpty(geom);

INSERT INTO territory.constraint_zone (id, kind, mos_dataset, mos_global_id, name, attrs, geom, area_m2,
                                       source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), kind, current_setting('ff.dataset')::int, global_id, name, attrs, geom, ST_Area(geom::geography),
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM src
ON CONFLICT (mos_dataset, mos_global_id) DO UPDATE SET
    kind = EXCLUDED.kind, name = EXCLUDED.name, attrs = EXCLUDED.attrs, geom = EXCLUDED.geom,
    area_m2 = EXCLUDED.area_m2, snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM territory.constraint_zone WHERE mos_dataset = current_setting('ff.dataset')::int AND last_seen_at < now();

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'zones' AS key, count(*) AS value FROM territory.constraint_zone WHERE mos_dataset = current_setting('ff.dataset')::int
UNION ALL SELECT 'zones_area_ha', round(sum(area_m2) / 10000)::bigint FROM territory.constraint_zone
          WHERE mos_dataset = current_setting('ff.dataset')::int;

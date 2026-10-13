-- Адресный реестр объектов недвижимости (набор 60562) → territory.address.

CREATE TEMP TABLE src ON COMMIT DROP AS
SELECT r.global_id, mos_stage.num(r.cells->'UNOM')::bigint AS unom,
       mos_stage.txt(r.cells->'ADDRESS') AS address, mos_stage.txt(r.cells->'SIMPLE_ADDRESS') AS simple_address,
       mos_stage.txt(r.cells->'OBJ_TYPE_PUBLIC') AS obj_type, mos_stage.txt(r.cells->'DISTRICT') AS municipal,
       mos_stage.txt(r.cells->'ADM_AREA') AS adm_area, mos_stage.txt(r.cells->'P6') AS settlement,
       mos_stage.txt(r.cells->'P7') AS street, mos_stage.txt(r.cells->'L1_VALUE') AS house,
       mos_stage.txt(r.cells->'L2_VALUE') AS korpus, mos_stage.txt(r.cells->'L3_VALUE') AS building,
       mos_stage.txt(r.cells->'STATUS') AS status, mos_stage.txt(r.cells->'N_FIAS') AS fias,
       coalesce((SELECT array_agg(k->>'KAD_N') FROM jsonb_array_elements(
                     CASE WHEN jsonb_typeof(r.cells->'KAD_N') = 'array' THEN r.cells->'KAD_N' ELSE '[]'::jsonb END) k
                 WHERE k->>'KAD_N' IS NOT NULL), '{}') AS cadastral,
       mos_stage.geom(r.geometry) AS geom
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int;

-- Учётный номер — ключ адреса; записи без номера или адреса не переносятся.
DELETE FROM src WHERE unom IS NULL OR address IS NULL;

-- Повтор учётного номера (редко) — берётся запись с большим global_id.
DELETE FROM src a USING src b WHERE a.unom = b.unom AND a.global_id < b.global_id;

INSERT INTO territory.address (unom, mos_global_id, address, simple_address, obj_type, municipal, adm_area, settlement,
                               street, house, korpus, building, status, fias, cadastral, geom, point,
                               source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT unom, global_id, address, simple_address, obj_type, municipal, adm_area, settlement, street, house, korpus,
       building, status, fias, cadastral, geom,
       CASE WHEN geom IS NULL THEN NULL WHEN GeometryType(geom) = 'POINT' THEN geom ELSE ST_PointOnSurface(geom) END,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM src
ON CONFLICT (unom) DO UPDATE SET
    mos_global_id = EXCLUDED.mos_global_id, address = EXCLUDED.address, simple_address = EXCLUDED.simple_address,
    obj_type = EXCLUDED.obj_type, municipal = EXCLUDED.municipal, adm_area = EXCLUDED.adm_area,
    settlement = EXCLUDED.settlement, street = EXCLUDED.street, house = EXCLUDED.house, korpus = EXCLUDED.korpus,
    building = EXCLUDED.building, status = EXCLUDED.status, fias = EXCLUDED.fias, cadastral = EXCLUDED.cadastral,
    geom = EXCLUDED.geom, point = EXCLUDED.point, snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM territory.address WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'addresses' AS key, count(*) AS value FROM territory.address
UNION ALL SELECT 'addresses_new', count(*) FROM territory.address WHERE first_seen_at = now()
UNION ALL SELECT 'addresses_without_geometry', count(*) FROM territory.address WHERE geom IS NULL
UNION ALL SELECT 'addresses_without_geometry_pct', coalesce(round(100.0 * count(*) FILTER (WHERE geom IS NULL) / nullif(count(*), 0)), 0)
          FROM territory.address
UNION ALL SELECT 'addresses_with_key', count(*) FROM territory.address WHERE address_key NOT LIKE '|%';

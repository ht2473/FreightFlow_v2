-- Разрешения на ввод объектов в эксплуатацию (набор 64028; проекция без сведений о
-- застройщике) → infra.commissioning. Место — по адресному реестру (ключ адреса: улица, дом,
-- корпус, строение; без точного совпадения — улица и дом); объект реестра — если точка
-- внутри его контура или ближе 100 м.

CREATE TEMP TABLE src ON COMMIT DROP AS
SELECT r.global_id, mos_stage.txt(r.cells->'PermissionNumber') AS permit_number,
       mos_stage.date(r.cells->>'PermissionDate') AS permit_date, mos_stage.txt(r.cells->'ObjectName') AS object_name,
       mos_stage.txt(r.cells->'Address') AS address, mos_stage.txt(r.cells->'AdmArea') AS adm_area,
       mos_stage.txt(r.cells->'District') AS district, mos_stage.txt(r.cells->'WorkType') AS work_type,
       mos_stage.txt(r.cells->'FunctionalPurpose') AS purpose, mos_stage.txt(r.cells->'FundingSource') AS funding,
       mos_stage.num(r.cells->'ObjectArea') AS area_m2, mos_stage.num(r.cells->'ObjectLength') AS length_m,
       territory.address_key(mos_stage.txt(r.cells->'Address')) AS akey
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int;

ALTER TABLE src ADD COLUMN logistics boolean, ADD COLUMN unom bigint;

UPDATE src SET logistics = concat_ws(' ', purpose, object_name) ~* '(склад|логистич|терминал|распределит|хранени|холодильн|оптов)';

UPDATE src s SET unom = (SELECT a.unom FROM territory.address a WHERE a.address_key = s.akey AND a.point IS NOT NULL LIMIT 1)
WHERE s.akey IS NOT NULL;

UPDATE src s SET unom = (
    SELECT a.unom FROM territory.address a
    WHERE a.address_key LIKE split_part(s.akey, '|', 1) || '|' || split_part(s.akey, '|', 2) || '|%' AND a.point IS NOT NULL
    ORDER BY a.address_key LIMIT 1)
WHERE s.unom IS NULL AND s.akey IS NOT NULL AND split_part(s.akey, '|', 1) <> '';

INSERT INTO infra.commissioning (id, mos_global_id, permit_number, permit_date, object_name, address, adm_area, district,
                                 work_type, purpose, funding, area_m2, length_m, logistics, address_unom, point,
                                 source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), s.global_id, s.permit_number, s.permit_date, s.object_name, s.address, s.adm_area, s.district,
       s.work_type, s.purpose, s.funding, s.area_m2, s.length_m, s.logistics, s.unom, a.point,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM src s
LEFT JOIN territory.address a ON a.unom = s.unom
ON CONFLICT (mos_global_id) DO UPDATE SET
    permit_number = EXCLUDED.permit_number, permit_date = EXCLUDED.permit_date, object_name = EXCLUDED.object_name,
    address = EXCLUDED.address, adm_area = EXCLUDED.adm_area, district = EXCLUDED.district,
    work_type = EXCLUDED.work_type, purpose = EXCLUDED.purpose, funding = EXCLUDED.funding,
    area_m2 = EXCLUDED.area_m2, length_m = EXCLUDED.length_m, logistics = EXCLUDED.logistics,
    address_unom = EXCLUDED.address_unom, point = EXCLUDED.point,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM infra.commissioning WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

UPDATE infra.commissioning c SET facility_id = (
    SELECT f.id FROM infra.facility f
    WHERE f.status = 'active' AND ST_DWithin(f.point::geography, c.point::geography, 600)
      AND (ST_Intersects(coalesce(f.territory, f.footprint), c.point) OR ST_DWithin(f.point::geography, c.point::geography, 100))
    ORDER BY f.point <-> c.point LIMIT 1)
WHERE c.point IS NOT NULL;

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'permits' AS key, count(*) AS value FROM infra.commissioning
UNION ALL SELECT 'permits_located', count(*) FROM infra.commissioning WHERE point IS NOT NULL
UNION ALL SELECT 'permits_logistics', count(*) FROM infra.commissioning WHERE logistics
UNION ALL SELECT 'permits_logistics_located', count(*) FROM infra.commissioning WHERE logistics AND point IS NOT NULL
UNION ALL SELECT 'permits_matched_facility', count(*) FROM infra.commissioning WHERE facility_id IS NOT NULL;

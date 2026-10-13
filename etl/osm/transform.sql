-- Перенос OpenStreetMap из osm_stage в домен. Выполняется одной транзакцией.
-- Параметры транзакции: ff.source — код источника, ff.snapshot — идентификатор снимка.
-- Записи, не встреченные в новом снимке, получают статус «missing» (объекты) или удаляются
-- (производные таблицы сети и инфраструктуры), идентификаторы сохраняются по osm_type/osm_id.

-- 1. Административные границы -------------------------------------------------

CREATE TEMP TABLE stage_boundary ON COMMIT DROP AS
SELECT osm_id, admin_level, tags, tags->>'name' AS name, ST_Multi(ST_CollectionExtract(ST_MakeValid(geom), 3)) AS geom
FROM osm_stage.boundaries
WHERE tags ? 'name';

CREATE INDEX ON stage_boundary USING gist (geom);

CREATE TEMP TABLE region ON COMMIT DROP AS
SELECT osm_id, name, geom FROM stage_boundary
WHERE admin_level = 4 AND name IN ('Москва', 'Московская область');

CREATE TEMP TABLE boundary_kind ON COMMIT DROP AS
SELECT b.osm_id, b.admin_level, b.name, b.tags, b.geom,
       CASE
           WHEN b.admin_level = 4 THEN 'region'
           WHEN b.admin_level = 5 AND r.name = 'Москва' THEN 'okrug'
           WHEN b.admin_level = 6 AND r.name = 'Московская область' THEN 'municipal_district'
           WHEN b.admin_level = 8 AND r.name = 'Москва' THEN 'district'
       END AS kind
FROM stage_boundary b
LEFT JOIN region r ON ST_Contains(r.geom, ST_PointOnSurface(b.geom))
WHERE b.admin_level = 4 AND b.name IN ('Москва', 'Московская область')
   OR b.admin_level IN (5, 6, 8) AND r.osm_id IS NOT NULL;

DELETE FROM boundary_kind WHERE kind IS NULL;

INSERT INTO territory.admin_area (id, kind, admin_level, name, short_name, osm_id, tags, geom, area_km2,
                                  source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), kind, admin_level, name,
       coalesce(tags->>'short_name', CASE name
           WHEN 'Центральный административный округ' THEN 'ЦАО'
           WHEN 'Северный административный округ' THEN 'САО'
           WHEN 'Северо-Восточный административный округ' THEN 'СВАО'
           WHEN 'Восточный административный округ' THEN 'ВАО'
           WHEN 'Юго-Восточный административный округ' THEN 'ЮВАО'
           WHEN 'Южный административный округ' THEN 'ЮАО'
           WHEN 'Юго-Западный административный округ' THEN 'ЮЗАО'
           WHEN 'Западный административный округ' THEN 'ЗАО'
           WHEN 'Северо-Западный административный округ' THEN 'СЗАО'
           WHEN 'Зеленоградский административный округ' THEN 'ЗелАО'
           WHEN 'Новомосковский административный округ' THEN 'НАО'
           WHEN 'Троицкий административный округ' THEN 'ТАО'
       END),
       osm_id, tags, geom, ST_Area(geom::geography) / 1e6,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM boundary_kind
ON CONFLICT (osm_id) DO UPDATE SET
    kind = EXCLUDED.kind, admin_level = EXCLUDED.admin_level, name = EXCLUDED.name,
    short_name = EXCLUDED.short_name, tags = EXCLUDED.tags, geom = EXCLUDED.geom,
    area_km2 = EXCLUDED.area_km2, snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

-- Родитель: округ и городской округ — в своём регионе, район — в округе.
UPDATE territory.admin_area c SET parent_id = p.id
FROM territory.admin_area p
WHERE c.last_seen_at = now() AND p.last_seen_at = now()
  AND ((c.kind IN ('okrug', 'municipal_district') AND p.kind = 'region')
    OR (c.kind = 'district' AND p.kind = 'okrug'))
  AND ST_Contains(p.geom, ST_PointOnSurface(c.geom));

DELETE FROM territory.admin_area
WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

-- 2. Кольца ---------------------------------------------------------------------

CREATE TEMP TABLE ring_line ON COMMIT DROP AS
WITH routes AS (
    SELECT CASE
               WHEN tags->>'name' = 'Садовое кольцо' THEN 'sk'
               WHEN tags->>'name' = 'Третье транспортное кольцо' OR tags->>'ref' = 'ТТК' THEN 'ttk'
               WHEN tags->>'name' IN ('Московская кольцевая автомобильная дорога', 'МКАД') OR tags->>'ref' = 'МКАД' THEN 'mkad'
               WHEN tags->>'name' IN ('Центральная кольцевая автомобильная дорога', 'ЦКАД')
                    OR tags->>'ref' IN ('ЦКАД', 'А-113', 'A-113') THEN 'ckad'
           END AS code,
           geom
    FROM osm_stage.routes
)
SELECT code, ST_Multi(ST_LineMerge(ST_Union(geom))) AS line
FROM routes
WHERE code IS NOT NULL
GROUP BY code;

-- Зона внутри кольца: линия расширяется на 60 м (закрывает разрывы на развязках),
-- наибольшая внутренняя граница — контур зоны, затем возврат к оси дороги.
CREATE TEMP TABLE ring_area ON COMMIT DROP AS
WITH buf AS (
    SELECT code, (ST_Dump(ST_Buffer(ST_Transform(line, 32637), 60))).geom AS poly FROM ring_line
), holes AS (
    SELECT code, (ST_DumpRings(poly)).path[1] AS idx, (ST_DumpRings(poly)).geom AS ring FROM buf
), best AS (
    SELECT DISTINCT ON (code) code, ring FROM holes WHERE idx > 0
    ORDER BY code, ST_Area(ring) DESC
)
SELECT code, ST_Multi(ST_Transform(ST_Buffer(ring, 60), 4326)) AS zone FROM best;

INSERT INTO territory.ring_zone (code, name, ord, line, zone, length_km, area_km2, source_code, snapshot_id, updated_at)
SELECT l.code,
       CASE l.code WHEN 'sk' THEN 'Садовое кольцо' WHEN 'ttk' THEN 'Третье транспортное кольцо'
                   WHEN 'mkad' THEN 'МКАД' ELSE 'ЦКАД' END,
       CASE l.code WHEN 'sk' THEN 1 WHEN 'ttk' THEN 2 WHEN 'mkad' THEN 3 ELSE 4 END,
       -- Длина кольца — по контуру зоны: линия маршрута включает обе проезжие части и съезды.
       l.line, a.zone,
       coalesce(ST_Length(ST_ExteriorRing(ST_GeometryN(a.zone, 1))::geography), ST_Length(l.line::geography) / 2) / 1000,
       ST_Area(a.zone::geography) / 1e6,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now()
FROM ring_line l
LEFT JOIN ring_area a USING (code)
ON CONFLICT (code) DO UPDATE SET
    line = EXCLUDED.line, zone = coalesce(EXCLUDED.zone, territory.ring_zone.zone),
    length_km = EXCLUDED.length_km, area_km2 = coalesce(EXCLUDED.area_km2, territory.ring_zone.area_km2),
    snapshot_id = EXCLUDED.snapshot_id, updated_at = now();

-- Область анализа: Москва и территория внутри ЦКАД (агломерация).
CREATE TEMP TABLE scope ON COMMIT DROP AS
SELECT ST_Union(geom) AS geom FROM (
    SELECT geom FROM territory.admin_area WHERE kind = 'region' AND name = 'Москва'
    UNION ALL
    SELECT zone FROM territory.ring_zone WHERE code = 'ckad' AND zone IS NOT NULL
) s;

CREATE TEMP TABLE moscow ON COMMIT DROP AS
SELECT geom FROM territory.admin_area WHERE kind = 'region' AND name = 'Москва';

-- 3. Промзоны -------------------------------------------------------------------

INSERT INTO infra.industrial_zone (id, name, geom, area_m2, tags, osm_type, osm_id, source_code, snapshot_id,
                                   first_seen_at, last_seen_at)
SELECT uuidv7(), a.tags->>'name', ST_Multi(ST_CollectionExtract(ST_MakeValid(a.geom), 3)),
       ST_Area(a.geom::geography), a.tags, a.osm_type, a.osm_id,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM osm_stage.areas a, scope s
WHERE a.kind = 'industrial' AND ST_Intersects(s.geom, a.geom)
ON CONFLICT (osm_type, osm_id) DO UPDATE SET
    name = EXCLUDED.name, geom = EXCLUDED.geom, area_m2 = EXCLUDED.area_m2, tags = EXCLUDED.tags,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM infra.industrial_zone
WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

-- 4. Точечная инфраструктура -------------------------------------------------------

CREATE TEMP TABLE stage_poi ON COMMIT DROP AS
SELECT a.kind, a.tags, a.osm_type, a.osm_id, ST_PointOnSurface(a.geom) AS point,
       ST_Multi(ST_CollectionExtract(ST_MakeValid(a.geom), 3)) AS area
FROM osm_stage.areas a
WHERE a.kind IN ('fuel', 'truck_parking', 'rest_area', 'marketplace', 'rail_station', 'rail_freight', 'customs', 'port')
UNION ALL
-- Точка без контура того же вида вокруг (иначе объект был бы учтён дважды).
SELECT p.kind, p.tags, 'N', p.osm_id, p.geom, NULL
FROM osm_stage.points p
WHERE p.kind IN ('fuel', 'truck_parking', 'rest_area', 'marketplace', 'rail_station', 'rail_freight', 'customs')
  AND NOT EXISTS (
      SELECT 1 FROM osm_stage.areas a WHERE a.kind = p.kind AND ST_Intersects(a.geom, p.geom)
  );

INSERT INTO infra.poi (id, kind, name, point, area, tags, osm_type, osm_id, source_code, snapshot_id,
                       first_seen_at, last_seen_at)
SELECT uuidv7(), p.kind, p.tags->>'name', p.point, p.area, p.tags, p.osm_type, p.osm_id,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM stage_poi p, scope s
WHERE ST_Intersects(s.geom, p.point)
ON CONFLICT (kind, osm_type, osm_id) DO UPDATE SET
    name = EXCLUDED.name, point = EXCLUDED.point, area = EXCLUDED.area, tags = EXCLUDED.tags,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM infra.poi WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

-- 5. Объекты логистики ---------------------------------------------------------------

-- Территории логистики: явная разметка, сортировочные центры, оптовые базы, порты.
CREATE TEMP TABLE fac_territory ON COMMIT DROP AS
SELECT a.osm_type, a.osm_id, a.kind, a.tags, ST_Multi(ST_CollectionExtract(ST_MakeValid(a.geom), 3)) AS geom
FROM osm_stage.areas a, scope s
WHERE a.kind IN ('logistics', 'post_depot', 'wholesale', 'port') AND ST_Intersects(s.geom, a.geom)
  AND ST_Area(a.geom::geography) >= 500;

CREATE INDEX ON fac_territory USING gist (geom);

-- Здания-кандидаты: от 300 м² застройки.
CREATE TEMP TABLE fac_building ON COMMIT DROP AS
SELECT a.osm_type, a.osm_id, a.tags, ST_Multi(ST_CollectionExtract(ST_MakeValid(a.geom), 3)) AS geom,
       ST_Area(a.geom::geography) AS area_m2,
       nullif(regexp_replace(coalesce(a.tags->>'building:levels', ''), '[^0-9].*$', ''), '')::int AS levels
FROM osm_stage.areas a, scope s
WHERE a.kind = 'building' AND ST_Intersects(s.geom, a.geom) AND ST_Area(a.geom::geography) >= 300;

CREATE INDEX ON fac_building USING gist (geom);

-- Здание внутри территории логистики относится к ней.
CREATE TEMP TABLE fac_building_in ON COMMIT DROP AS
SELECT DISTINCT ON (b.osm_type, b.osm_id) b.osm_type, b.osm_id, t.osm_type AS t_type, t.osm_id AS t_id
FROM fac_building b
JOIN fac_territory t ON ST_Contains(t.geom, ST_PointOnSurface(b.geom))
ORDER BY b.osm_type, b.osm_id, ST_Area(t.geom);

CREATE TEMP TABLE fac_candidate ON COMMIT DROP AS
-- Территории со своими зданиями.
SELECT t.osm_type, t.osm_id, t.kind, t.tags, t.geom AS territory,
       ST_Multi(ST_Union(b.geom)) AS footprint, count(b.osm_id) AS buildings,
       coalesce(sum(b.area_m2), 0) AS building_area, max(b.levels) AS levels,
       bool_or(b.tags->>'building' IN ('warehouse', 'logistics', 'storage', 'cold_storage')) AS has_storage_building
FROM fac_territory t
LEFT JOIN fac_building_in i ON i.t_type = t.osm_type AND i.t_id = t.osm_id
LEFT JOIN fac_building b ON b.osm_type = i.osm_type AND b.osm_id = i.osm_id
GROUP BY t.osm_type, t.osm_id, t.kind, t.tags, t.geom
UNION ALL
-- Отдельные складские здания (промышленные корпуса без складских признаков не берутся).
SELECT b.osm_type, b.osm_id, 'building', b.tags, NULL, b.geom, 1, b.area_m2, b.levels,
       b.tags->>'building' IN ('warehouse', 'logistics', 'storage', 'cold_storage')
FROM fac_building b
WHERE NOT EXISTS (SELECT 1 FROM fac_building_in i WHERE i.osm_type = b.osm_type AND i.osm_id = b.osm_id)
  AND (b.tags->>'building' IN ('warehouse', 'logistics', 'storage', 'cold_storage', 'depot')
       OR b.tags->>'building:use' = 'warehouse'
       OR coalesce(b.tags->>'name', '') ~* '(склад|логист|терминал|распредел)')
UNION ALL
-- Точки без контура (узлы с разметкой логистики).
SELECT 'N', p.osm_id, p.kind, p.tags, NULL, NULL, 0, 0, NULL, false
FROM osm_stage.points p, scope s
WHERE p.kind IN ('logistics', 'post_depot', 'wholesale') AND ST_Intersects(s.geom, p.geom)
  AND NOT EXISTS (SELECT 1 FROM fac_territory t WHERE ST_Intersects(t.geom, p.geom))
  AND NOT EXISTS (SELECT 1 FROM fac_building b WHERE ST_Intersects(b.geom, p.geom));

ALTER TABLE fac_candidate ADD COLUMN point geometry(Point, 4326);
UPDATE fac_candidate c SET point = coalesce(
    ST_PointOnSurface(c.footprint), ST_PointOnSurface(c.territory),
    (SELECT p.geom FROM osm_stage.points p WHERE c.osm_type = 'N' AND p.osm_id = c.osm_id LIMIT 1));
DELETE FROM fac_candidate WHERE point IS NULL;

-- Вложенные и перекрывающиеся контуры — один объект: остаётся больший, если меньший лежит в нём
-- больше чем наполовину (территория внутри территории, корпус с отдельной разметкой).
DELETE FROM fac_candidate c
USING fac_candidate o
WHERE (o.osm_type, o.osm_id) <> (c.osm_type, c.osm_id)
  AND coalesce(o.territory, o.footprint) IS NOT NULL
  AND ST_Intersects(coalesce(o.territory, o.footprint), c.point)
  AND ST_Area(coalesce(o.territory, o.footprint)) > coalesce(ST_Area(coalesce(c.territory, c.footprint)), 0)
  AND (coalesce(c.territory, c.footprint) IS NULL
       OR ST_Area(ST_Intersection(coalesce(o.territory, o.footprint), coalesce(c.territory, c.footprint)))
          > 0.5 * ST_Area(coalesce(c.territory, c.footprint)));

-- Тип, достоверность и её причины.
CREATE TEMP TABLE fac_scored ON COMMIT DROP AS
WITH x AS (
    SELECT c.*,
           coalesce(c.tags->>'name', '') AS nm,
           EXISTS (SELECT 1 FROM infra.industrial_zone z WHERE z.last_seen_at = now() AND ST_Contains(z.geom, c.point)) AS in_zone
    FROM fac_candidate c
)
SELECT x.*,
       CASE
           WHEN x.kind = 'post_depot' THEN 'sorting_center'
           WHEN x.kind = 'wholesale' THEN 'wholesale'
           WHEN x.kind = 'port' THEN 'port'
           WHEN x.nm ~* '(холодильн|хладо|морозил|cold)' OR x.tags->>'building' = 'cold_storage' THEN 'cold_storage'
           WHEN x.nm ~* '(терминал|terminal)' THEN 'terminal'
           WHEN x.nm ~* '(логистич|распредел|\mрц\M|\mdc\M|хаб|\mhub\M|фулфил|fulfil)'
                OR (x.buildings >= 2 AND x.building_area >= 10000) THEN 'logistics_center'
           ELSE 'warehouse'
       END AS type_code,
       array_remove(ARRAY[
           CASE WHEN x.kind IN ('logistics', 'post_depot', 'wholesale', 'port')
                THEN jsonb_build_object('code', 'tag', 'weight', 0.5, 'text', 'Явная разметка назначения в OSM') END,
           CASE WHEN x.kind = 'building' AND x.has_storage_building
                THEN jsonb_build_object('code', 'building', 'weight', 0.45, 'text', 'Здание отмечено как складское') END,
           CASE WHEN x.kind <> 'building' AND x.has_storage_building
                THEN jsonb_build_object('code', 'building', 'weight', 0.1, 'text', 'На территории есть складские здания') END,
           CASE WHEN x.nm ~* '(склад|логист|терминал|распредел|хаб|фулфил|холодильн|\mрц\M)'
                THEN jsonb_build_object('code', 'name', 'weight', 0.2, 'text', 'Название указывает на склад или логистику') END,
           CASE WHEN x.building_area >= 2000
                THEN jsonb_build_object('code', 'area', 'weight', 0.15, 'text', 'Площадь застройки от 2 000 м²') END,
           CASE WHEN x.building_area >= 10000
                THEN jsonb_build_object('code', 'large', 'weight', 0.1, 'text', 'Площадь застройки от 10 000 м²') END,
           CASE WHEN x.in_zone
                THEN jsonb_build_object('code', 'zone', 'weight', 0.1, 'text', 'Расположен в промзоне') END,
           CASE WHEN x.tags ? 'operator' OR x.tags ? 'brand'
                THEN jsonb_build_object('code', 'operator', 'weight', 0.05, 'text', 'Указан оператор') END
       ], NULL) AS reasons
FROM x;

INSERT INTO infra.facility (id, type_code, name, status, point, footprint, territory, building_area_m2,
                            territory_area_m2, buildings, levels, floor_area_m2, address, confidence,
                            confidence_reasons, attrs, osm_type, osm_id, source_code, snapshot_id,
                            first_seen_at, last_seen_at, updated_at)
SELECT uuidv7(), f.type_code, nullif(f.nm, ''), 'active', f.point, f.footprint, f.territory,
       nullif(round(f.building_area::numeric, 0), 0),
       CASE WHEN f.territory IS NOT NULL THEN round(ST_Area(f.territory::geography)::numeric, 0) END,
       f.buildings, f.levels,
       nullif(round((f.building_area * coalesce(f.levels, 1))::numeric, 0), 0),
       nullif(concat_ws(', ', f.tags->>'addr:street', f.tags->>'addr:housenumber'), ''),
       least(1.0, (SELECT coalesce(sum((r->>'weight')::numeric), 0) FROM unnest(f.reasons) r))::real,
       to_jsonb(f.reasons),
       jsonb_strip_nulls(jsonb_build_object(
           'operator', f.tags->>'operator', 'brand', f.tags->>'brand', 'website', f.tags->>'website',
           'opening_hours', f.tags->>'opening_hours', 'building', f.tags->>'building')),
       f.osm_type, f.osm_id, current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now(), now()
FROM fac_scored f
ON CONFLICT (osm_type, osm_id) DO UPDATE SET
    type_code = EXCLUDED.type_code, name = EXCLUDED.name,
    status = CASE WHEN infra.facility.status = 'excluded' THEN 'excluded' ELSE 'active' END,
    point = EXCLUDED.point, footprint = EXCLUDED.footprint, territory = EXCLUDED.territory,
    building_area_m2 = EXCLUDED.building_area_m2, territory_area_m2 = EXCLUDED.territory_area_m2,
    buildings = EXCLUDED.buildings, levels = EXCLUDED.levels, floor_area_m2 = EXCLUDED.floor_area_m2,
    address = EXCLUDED.address, confidence = EXCLUDED.confidence, confidence_reasons = EXCLUDED.confidence_reasons,
    attrs = EXCLUDED.attrs, snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now(), updated_at = now();

UPDATE infra.facility SET status = 'missing', updated_at = now()
WHERE source_code = current_setting('ff.source') AND last_seen_at < now() AND status = 'active';

-- Официальные записи (адресный реестр, разрешения на ввод): достоверность из OSM пересчитана
-- заново, причины по официальным совпадениям добавляются снова.
SELECT infra.enrich_facilities();

-- Привязка к территориям и кольцам.
UPDATE infra.facility f SET
    district_id = (SELECT a.id FROM territory.admin_area a
                   WHERE a.kind IN ('district', 'municipal_district') AND ST_Contains(a.geom, f.point)
                   ORDER BY a.admin_level DESC LIMIT 1),
    okrug_id = (SELECT a.id FROM territory.admin_area a WHERE a.kind = 'okrug' AND ST_Contains(a.geom, f.point) LIMIT 1),
    region_id = (SELECT a.id FROM territory.admin_area a WHERE a.kind = 'region' AND ST_Contains(a.geom, f.point) LIMIT 1),
    ring = coalesce((SELECT r.code FROM territory.ring_zone r
                     WHERE r.zone IS NOT NULL AND ST_Contains(r.zone, f.point) ORDER BY r.ord LIMIT 1), 'outside')
WHERE f.last_seen_at = now();

-- 6. Дорожная сеть ---------------------------------------------------------------------

-- В Москве — вся сеть; в области внутри ЦКАД — дороги от местного значения и выше.
CREATE TEMP TABLE stage_road ON COMMIT DROP AS
SELECT r.osm_id, r.class, r.tags, r.geom
FROM osm_stage.roads r, scope s, moscow m
WHERE ST_Intersects(s.geom, r.geom)
  AND (r.class NOT IN ('service', 'track', 'living_street', 'residential') OR ST_Intersects(m.geom, r.geom));

INSERT INTO network.road_segment (id, osm_id, class, name, ref, oneway, lanes, maxspeed, bridge, tunnel, layer,
                                  length_m, tags, geom, source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), r.osm_id, r.class, r.tags->>'name', r.tags->>'ref',
       CASE WHEN r.tags->>'oneway' IN ('yes', '1', 'true') OR r.tags->>'junction' = 'roundabout'
                 OR r.tags->>'highway' = 'motorway' AND coalesce(r.tags->>'oneway', '') <> 'no' THEN 1
            WHEN r.tags->>'oneway' = '-1' THEN -1 ELSE 0 END,
       nullif(substring(r.tags->>'lanes' FROM '^[0-9]+'), '')::smallint,
       nullif(substring(r.tags->>'maxspeed' FROM '^[0-9]+'), '')::smallint,
       coalesce(r.tags->>'bridge', 'no') NOT IN ('no', '0', 'false'),
       coalesce(r.tags->>'tunnel', 'no') NOT IN ('no', '0', 'false'),
       coalesce(nullif(substring(r.tags->>'layer' FROM '^-?[0-9]+'), '')::smallint, 0),
       ST_Length(r.geom::geography), r.tags, r.geom,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM stage_road r
ON CONFLICT (osm_id) DO UPDATE SET
    class = EXCLUDED.class, name = EXCLUDED.name, ref = EXCLUDED.ref, oneway = EXCLUDED.oneway,
    lanes = EXCLUDED.lanes, maxspeed = EXCLUDED.maxspeed, bridge = EXCLUDED.bridge, tunnel = EXCLUDED.tunnel,
    layer = EXCLUDED.layer, length_m = EXCLUDED.length_m, tags = EXCLUDED.tags, geom = EXCLUDED.geom,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM network.road_segment WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

UPDATE network.road_segment s SET ring = coalesce(
    (SELECT r.code FROM territory.ring_zone r
     WHERE r.zone IS NOT NULL AND ST_Contains(r.zone, ST_LineInterpolatePoint(s.geom, 0.5)) ORDER BY r.ord LIMIT 1),
    'outside')
WHERE s.last_seen_at = now();

-- Ограничения из тегов: величины по направлениям, запреты для грузовиков, условные теги.
DELETE FROM network.restriction WHERE origin = 'osm';

INSERT INTO network.restriction (id, segment_id, kind, value, unit, direction, condition, raw, origin, source_code, snapshot_id)
SELECT uuidv7(), s.id, m.kind, network.parse_measure(t.value), m.unit, m.direction, NULL, t.value, 'osm',
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM network.road_segment s
CROSS JOIN LATERAL jsonb_each_text(s.tags) AS t(key, value)
JOIN (VALUES
    ('maxweight', 'maxweight', 't', 0), ('maxweight:forward', 'maxweight', 't', 1), ('maxweight:backward', 'maxweight', 't', -1),
    ('maxweight:hgv', 'maxweight', 't', 0),
    ('maxaxleload', 'maxaxleload', 't', 0),
    ('maxheight', 'maxheight', 'm', 0), ('maxheight:forward', 'maxheight', 'm', 1), ('maxheight:backward', 'maxheight', 'm', -1),
    ('maxheight:physical', 'maxheight', 'm', 0),
    ('maxwidth', 'maxwidth', 'm', 0), ('maxlength', 'maxlength', 'm', 0)
) AS m(tag, kind, unit, direction) ON m.tag = t.key
WHERE s.last_seen_at = now() AND network.parse_measure(t.value) IS NOT NULL;

INSERT INTO network.restriction (id, segment_id, kind, direction, raw, origin, source_code, snapshot_id)
SELECT uuidv7(), s.id,
       CASE WHEN s.tags->>'hgv' = 'no' THEN 'hgv_ban' ELSE 'hgv_destination' END,
       0, 'hgv=' || (s.tags->>'hgv'), 'osm', current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM network.road_segment s
WHERE s.last_seen_at = now() AND s.tags->>'hgv' IN ('no', 'destination', 'delivery');

INSERT INTO network.restriction (id, segment_id, kind, direction, raw, origin, source_code, snapshot_id)
SELECT uuidv7(), s.id, 'motor_vehicle_ban', 0, 'motor_vehicle=no', 'osm',
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM network.road_segment s
WHERE s.last_seen_at = now() AND s.tags->>'motor_vehicle' = 'no';

-- Условные теги: «no @ (Mo-Fr 07:00-22:00)», несколько условий через «;».
INSERT INTO network.restriction (id, segment_id, kind, value, unit, direction, condition, raw, origin, source_code, snapshot_id)
SELECT uuidv7(), s.id,
       CASE WHEN c.base = 'hgv' AND trim(c.val) = 'no' THEN 'hgv_ban'
            WHEN c.base = 'hgv' THEN 'hgv_destination'
            ELSE c.base END,
       CASE WHEN c.base <> 'hgv' THEN network.parse_measure(c.val) END,
       CASE WHEN c.base IN ('maxweight', 'maxaxleload') THEN 't' WHEN c.base <> 'hgv' THEN 'm' END,
       0, trim(both ' ()' FROM c.cond), c.raw, 'osm', current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM network.road_segment s
CROSS JOIN LATERAL (
    SELECT split_part(t.key, ':', 1) AS base, t.value AS raw,
           split_part(part, '@', 1) AS val, split_part(part, '@', 2) AS cond
    FROM jsonb_each_text(s.tags) AS t(key, value)
    CROSS JOIN LATERAL regexp_split_to_table(t.value, ';(?![^(]*\))') AS part
    WHERE t.key IN ('hgv:conditional', 'maxweight:conditional', 'maxheight:conditional', 'maxaxleload:conditional')
) c
WHERE s.last_seen_at = now() AND c.cond <> ''
  AND (c.base = 'hgv' AND trim(c.val) IN ('no', 'destination', 'delivery')
       OR c.base <> 'hgv' AND network.parse_measure(c.val) IS NOT NULL);

-- Знаки портала Москвы заново привязываются к обновлённой сети (до первой загрузки знаков
-- функция ничего не делает).
SELECT network.match_signs();

-- Мосты и путепроводы.
DELETE FROM network.bridge WHERE source_code = current_setting('ff.source');

INSERT INTO network.bridge (id, segment_id, name, kind, length_m, maxweight_t, maxheight_m, geom, osm_id, source_code, snapshot_id)
SELECT uuidv7(), s.id, coalesce(s.tags->>'bridge:name', s.name),
       CASE s.tags->>'bridge' WHEN 'viaduct' THEN 'viaduct' ELSE 'bridge' END,
       s.length_m,
       network.parse_measure(s.tags->>'maxweight'), network.parse_measure(s.tags->>'maxheight'),
       s.geom, s.osm_id, current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM network.road_segment s
WHERE s.last_seen_at = now() AND s.bridge AND s.class NOT IN ('service', 'track');

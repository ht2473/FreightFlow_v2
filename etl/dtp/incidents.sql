-- ДТП (временная таблица dtp_stage) → operations.incident. Время в выгрузке — московское.
-- Грузовые ТС — по категориям классификации ГИБДД: грузовые автомобили, тягачи, самосвалы,
-- рефрижераторы, прицепы, цистерны, эвакуаторы, спецтехника; фургоны — грузовые, но не тяжёлые.

DELETE FROM operations.incident WHERE source_code = current_setting('ff.source');

INSERT INTO operations.incident (id, source_id, occurred_at, category, severity, dead, injured, participants, light,
                                 weather, road_conditions, nearby, address, district, vehicle_categories, freight,
                                 heavy_freight, point, source_code, snapshot_id)
SELECT DISTINCT ON (s.source_id)
       uuidv7(), s.source_id, s.datetime::timestamp AT TIME ZONE 'Europe/Moscow', nullif(s.category, ''),
       CASE s.severity WHEN 'С погибшими' THEN 'fatal' WHEN 'Тяжёлый' THEN 'severe' WHEN 'Легкий' THEN 'light' ELSE 'none' END,
       s.dead, s.injured, s.participants, nullif(s.light, ''),
       coalesce(s.weather, '{}'), coalesce(s.road_conditions, '{}'), coalesce(s.nearby, '{}'),
       nullif(s.address, ''), nullif(s.district, ''), coalesce(s.vehicles, '{}'),
       EXISTS (SELECT 1 FROM unnest(s.vehicles) c
               WHERE c ~* '(грузов|тягач|самосвал|рефрижератор|прицеп|цистерн|эвакуатор|бортов|спецтехник|фургон)'),
       EXISTS (SELECT 1 FROM unnest(s.vehicles) c
               WHERE c ~* '(грузов|тягач|самосвал|рефрижератор|прицеп|цистерн|эвакуатор|бортов|спецтехник)'),
       ST_SetSRID(ST_MakePoint(s.lon, s.lat), 4326), current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM dtp_stage s
WHERE s.datetime ~ '^\d{4}-\d{2}-\d{2} \d{2}:\d{2}'
ORDER BY s.source_id;

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'incidents' AS key, count(*) AS value FROM operations.incident WHERE source_code = current_setting('ff.source')
UNION ALL SELECT 'incidents_fatal', count(*) FROM operations.incident WHERE source_code = current_setting('ff.source') AND severity = 'fatal'
UNION ALL SELECT 'incidents_freight', count(*) FROM operations.incident WHERE source_code = current_setting('ff.source') AND freight
UNION ALL SELECT 'incidents_heavy_freight', count(*) FROM operations.incident WHERE source_code = current_setting('ff.source') AND heavy_freight
UNION ALL SELECT 'incidents_outside_moscow', count(*) FROM operations.incident i
          WHERE i.source_code = current_setting('ff.source')
            AND NOT EXISTS (SELECT 1 FROM territory.admin_area a WHERE a.kind = 'region' AND a.name = 'Москва' AND ST_Intersects(a.geom, i.point))
UNION ALL SELECT 'first_year', extract(year FROM min(occurred_at)) FROM operations.incident WHERE source_code = current_setting('ff.source')
UNION ALL SELECT 'last_year', extract(year FROM max(occurred_at)) FROM operations.incident WHERE source_code = current_setting('ff.source');

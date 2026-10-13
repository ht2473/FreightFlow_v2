-- Ряды по городу: среднегодовая численность населения (62021) → territory.population и
-- statistics.observation; выбросы загрязняющих веществ от автотранспорта (2385) →
-- statistics.observation.

INSERT INTO statistics.indicator (code, name, unit, periodicity, source_code, description)
SELECT * FROM (VALUES
    ('population_avg', 'Численность постоянного населения в среднем за год', 'человек', 'year',
     'mos_population', 'Портал открытых данных Москвы, набор 62021'),
    ('vehicle_emissions', 'Выбросы загрязняющих веществ в атмосферу от автомобильного транспорта', '', 'year',
     'mos_emissions', 'Портал открытых данных Москвы, набор 2385. Единица измерения в паспорте набора не указана.')
) v(code, name, unit, periodicity, source_code, description)
WHERE v.source_code = current_setting('ff.source')
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, unit = EXCLUDED.unit, description = EXCLUDED.description;

CREATE TEMP TABLE obs ON COMMIT DROP AS
SELECT CASE current_setting('ff.dataset')::int WHEN 62021 THEN 'population_avg' ELSE 'vehicle_emissions' END AS code,
       make_date(mos_stage.num(coalesce(r.cells->'Year', r.cells->'ReportYear'))::int, 1, 1) AS period,
       mos_stage.num(coalesce(r.cells->'Value', r.cells->'EmissionsOfPollutants')) AS value
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int;

DELETE FROM obs WHERE period IS NULL OR value IS NULL;

DELETE FROM statistics.observation WHERE source_code = current_setting('ff.source');

INSERT INTO statistics.observation (indicator_code, area_id, area_name, period, value, source_code, snapshot_id)
SELECT o.code, a.id, 'Москва', o.period, o.value, current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM obs o
LEFT JOIN territory.admin_area a ON a.kind = 'region' AND a.name = 'Москва';

DELETE FROM territory.population WHERE source_code = current_setting('ff.source');

INSERT INTO territory.population (area_id, area_name, year, value, kind, source_code, snapshot_id)
SELECT a.id, 'Москва', extract(year FROM o.period)::smallint, o.value::int, 'average',
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM obs o
LEFT JOIN territory.admin_area a ON a.kind = 'region' AND a.name = 'Москва'
WHERE o.code = 'population_avg';

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'observations' AS key, count(*) AS value FROM obs
UNION ALL SELECT 'first_year', min(extract(year FROM period))::bigint FROM obs
UNION ALL SELECT 'last_year', max(extract(year FROM period))::bigint FROM obs;

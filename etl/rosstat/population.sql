-- Численность населения муниципальных образований Москвы на 1 января (Росстат, БД ПМО,
-- показатель 8112027) → territory.population и statistics.observation. Записи — во временной
-- таблице pmo_stage (oktmo, name, year, value). Муниципальное образование сопоставляется с
-- районом по ключу названия; город целиком — строка «Москва».

INSERT INTO statistics.indicator (code, name, unit, periodicity, source_code, description)
VALUES ('population_jan1', 'Численность населения на 1 января', 'человек', 'year', current_setting('ff.source'),
        'Росстат, база данных показателей муниципальных образований, показатель 8112027')
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description;

CREATE TEMP TABLE pmo_area ON COMMIT DROP AS
SELECT DISTINCT ON (p.oktmo) p.oktmo, p.name,
       (SELECT a.id FROM territory.admin_area a
        WHERE (p.name = 'Москва' AND a.kind = 'region' AND a.name = 'Москва')
           OR (p.name <> 'Москва' AND a.kind = 'district' AND territory.area_name_key(a.name) = territory.area_name_key(p.name))
        LIMIT 1) AS area_id
FROM pmo_stage p;

DELETE FROM territory.population WHERE source_code = current_setting('ff.source');

INSERT INTO territory.population (area_id, area_name, year, value, kind, oktmo, source_code, snapshot_id)
SELECT a.area_id, p.name, p.year, p.value, 'january1', p.oktmo, current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM pmo_stage p
JOIN pmo_area a USING (oktmo);

DELETE FROM statistics.observation WHERE source_code = current_setting('ff.source');

INSERT INTO statistics.observation (indicator_code, area_id, area_name, period, value, source_code, snapshot_id)
SELECT 'population_jan1', a.area_id, p.name, make_date(p.year, 1, 1), p.value,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM pmo_stage p
JOIN pmo_area a USING (oktmo);

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'records' AS key, count(*) AS value FROM pmo_stage
UNION ALL SELECT 'areas', count(*) FROM pmo_area
UNION ALL SELECT 'areas_matched', count(*) FROM pmo_area WHERE area_id IS NOT NULL
UNION ALL SELECT 'last_year', max(year) FROM pmo_stage
UNION ALL SELECT 'moscow_last_year', (SELECT value FROM pmo_stage WHERE name = 'Москва' ORDER BY year DESC LIMIT 1);

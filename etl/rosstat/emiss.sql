-- Показатели ЕМИСС по Москве (временная таблица emiss_stage: indicator, name, unit, year,
-- value) → statistics.indicator и statistics.observation. Код показателя системы —
-- emiss_<номер показателя ЕМИСС>.

INSERT INTO statistics.indicator (code, name, unit, periodicity, source_code, description)
SELECT DISTINCT ON (indicator) 'emiss_' || indicator, name, unit, 'year', current_setting('ff.source'),
       'ЕМИСС (fedstat.ru), показатель ' || indicator
FROM emiss_stage
ORDER BY indicator, year DESC
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, unit = EXCLUDED.unit, description = EXCLUDED.description;

DELETE FROM statistics.observation WHERE source_code = current_setting('ff.source');

INSERT INTO statistics.observation (indicator_code, area_id, area_name, period, value, source_code, snapshot_id)
SELECT 'emiss_' || s.indicator, a.id, 'Москва', make_date(s.year, 1, 1), s.value,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM emiss_stage s
LEFT JOIN territory.admin_area a ON a.kind = 'region' AND a.name = 'Москва';

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'records' AS key, count(*) AS value FROM emiss_stage
UNION ALL SELECT 'indicators', count(DISTINCT indicator) FROM emiss_stage
UNION ALL SELECT 'first_year', min(year) FROM emiss_stage
UNION ALL SELECT 'last_year', max(year) FROM emiss_stage;

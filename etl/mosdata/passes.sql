-- Пропуска грузового транспорта (набор 3247; проекция без номера) → помесячные агрегаты
-- regulation.pass_stat по зонам и сериям. Действующим на первое число месяца считается пропуск
-- с началом не позже этой даты и окончанием (аннулированием) не раньше неё.

CREATE TEMP TABLE pass ON COMMIT DROP AS
SELECT coalesce(mos_stage.txt(cells->'Zone'), 'не указана') AS zone,
       coalesce(mos_stage.txt(cells->'Series'), 'не указана') AS series,
       mos_stage.date(cells->>'ValidFrom') AS vf,
       mos_stage.date(cells->>'ValidTo') AS vt,
       mos_stage.date(cells->>'CancelDate') AS cd
FROM mos_stage.record
WHERE dataset = current_setting('ff.dataset')::int;

DELETE FROM pass WHERE vf IS NULL OR vt IS NULL;

-- Фактическое окончание: аннулирование раньше срока сокращает действие (день до аннулирования).
CREATE TEMP TABLE span ON COMMIT DROP AS
SELECT zone, series,
       (date_trunc('month', vf - 1) + interval '1 month')::date AS first_month,
       date_trunc('month', least(vt, coalesce(cd - 1, vt)))::date AS last_month
FROM pass;

DELETE FROM regulation.pass_stat WHERE source_code = current_setting('ff.source');

-- Действующие: накопленная сумма «вошли в счёт» минус «вышли из счёта» по месяцам.
INSERT INTO regulation.pass_stat (month, zone, series, metric, value, source_code, snapshot_id)
WITH bounds AS (
    SELECT min(first_month) AS lo, date_trunc('month', now())::date AS hi FROM span WHERE first_month <= last_month
), months AS (
    SELECT g::date AS month FROM bounds, generate_series(lo, hi, interval '1 month') g
), delta AS (
    SELECT zone, series, first_month AS month, count(*) AS d FROM span WHERE first_month <= last_month GROUP BY 1, 2, 3
    UNION ALL
    SELECT zone, series, (last_month + interval '1 month')::date, -count(*) FROM span WHERE first_month <= last_month GROUP BY 1, 2, 3
), keys AS (
    SELECT DISTINCT zone, series FROM span
), grid AS (
    SELECT k.zone, k.series, m.month, coalesce(sum(d.d), 0) AS d
    FROM keys k CROSS JOIN months m
    LEFT JOIN delta d ON d.zone = k.zone AND d.series = k.series AND d.month = m.month
    GROUP BY 1, 2, 3
)
SELECT month, zone, series, 'valid', sum(d) OVER (PARTITION BY zone, series ORDER BY month)::int,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM grid;

DELETE FROM regulation.pass_stat WHERE source_code = current_setting('ff.source') AND metric = 'valid' AND value = 0;

INSERT INTO regulation.pass_stat (month, zone, series, metric, value, source_code, snapshot_id)
SELECT date_trunc('month', vf)::date, zone, series, 'issued', count(*),
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM pass GROUP BY 1, 2, 3;

INSERT INTO regulation.pass_stat (month, zone, series, metric, value, source_code, snapshot_id)
SELECT date_trunc('month', cd)::date, zone, series, 'cancelled', count(*),
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM pass WHERE cd IS NOT NULL GROUP BY 1, 2, 3;

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'passes' AS key, count(*) AS value FROM pass
UNION ALL SELECT 'passes_valid_now', coalesce(sum(value), 0) FROM regulation.pass_stat
          WHERE metric = 'valid' AND month = date_trunc('month', now())::date
UNION ALL SELECT 'zones', count(DISTINCT zone) FROM pass
UNION ALL SELECT 'series', count(DISTINCT series) FROM pass
UNION ALL SELECT 'months', count(DISTINCT month) FROM regulation.pass_stat WHERE metric = 'valid';

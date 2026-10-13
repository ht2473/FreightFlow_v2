-- Выгрузка записей набора портала Москвы в границах Нижегородского района (как фрагмент OSM в
-- ../osm/testdata) для тестов переноса в домен. Записи берутся из mos_stage.record после
-- загрузки набора; формат — JSON Lines снимка (id, cells, geometry). Запуск на базе разработки:
--   docker exec -i ffv2-dev-db-1 psql -U freightflow -d freightflow -At -v dataset=62681 -f - < make_records.sql | gzip -9 > signs.jsonl.gz
--   docker exec -i ffv2-dev-db-1 psql -U freightflow -d freightflow -At -v dataset=62101 -f - < make_records.sql | gzip -9 > works.jsonl.gz
-- Данные: Портал открытых данных Правительства Москвы, data.mos.ru (CC BY 4.0).

WITH d AS (
    SELECT ST_Envelope(geom) AS env FROM territory.admin_area WHERE name = 'Нижегородский район' AND kind = 'district'
)
SELECT json_build_object('id', r.global_id, 'cells', r.cells, 'geometry', r.geometry)
FROM mos_stage.record r, d
WHERE r.dataset = :dataset
  AND r.geometry IS NOT NULL
  AND ST_Intersects(mos_stage.geom(r.geometry), d.env)
ORDER BY r.global_id;

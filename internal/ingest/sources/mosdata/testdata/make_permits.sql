-- Выгрузка разрешений на ввод (набор 64028, без геометрии) по Нижегородскому району для теста
-- обогащения реестра объектов; формат — JSON Lines снимка. Запуск на базе разработки:
--   docker exec -i ffv2-dev-db-1 psql -U freightflow -d freightflow -At -f - < make_permits.sql | gzip -9 > permits.jsonl.gz
-- Данные: Портал открытых данных Правительства Москвы, data.mos.ru (CC BY 4.0).

SELECT json_build_object('id', r.global_id, 'cells', r.cells)
FROM mos_stage.record r
WHERE r.dataset = 64028 AND r.cells->>'District' ILIKE '%Нижегородск%'
ORDER BY r.global_id;

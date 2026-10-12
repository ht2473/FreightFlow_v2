-- Выгрузка фрагмента osm_stage для теста преобразования (transform_test.go): Нижегородский
-- район Москвы после импорта OSM. Запуск на базе разработки после `etl run osm`:
--   docker exec -i ffv2-dev-db-1 psql -U freightflow -d freightflow -At -f - < make_stage.sql | gzip -9 > stage.sql.gz
-- Границы Москвы и области упрощены до 0,002° (около 200 м): тесту нужна только принадлежность.

\set district 'Нижегородский район'

WITH d AS (
    SELECT ST_Envelope(geom) AS env FROM territory.admin_area WHERE name = :'district' AND kind = 'district'
)
SELECT '-- Фрагмент osm_stage: ' || :'district' || ' (Москва). Данные © участники OpenStreetMap, ODbL 1.0.'
UNION ALL SELECT $$
CREATE TABLE osm_stage.points (osm_id bigint NOT NULL, kind text NOT NULL, tags jsonb, geom geometry(Point, 4326) NOT NULL);
CREATE TABLE osm_stage.roads (osm_id bigint NOT NULL, class text NOT NULL, tags jsonb, geom geometry(LineString, 4326) NOT NULL);
CREATE TABLE osm_stage.rails (osm_id bigint NOT NULL, kind text NOT NULL, tags jsonb, geom geometry(LineString, 4326) NOT NULL);
CREATE TABLE osm_stage.areas (osm_type char(1) NOT NULL, osm_id bigint NOT NULL, kind text NOT NULL, tags jsonb, geom geometry(MultiPolygon, 4326) NOT NULL);
CREATE TABLE osm_stage.boundaries (osm_id bigint NOT NULL, admin_level integer NOT NULL, tags jsonb, geom geometry(MultiPolygon, 4326) NOT NULL);
CREATE TABLE osm_stage.routes (osm_id bigint NOT NULL, tags jsonb, geom geometry(MultiLineString, 4326) NOT NULL);$$
UNION ALL
SELECT 'INSERT INTO osm_stage.points VALUES ' || string_agg(format('(%s,%L,%L,%L)', osm_id, kind, tags, encode(ST_AsEWKB(geom), 'hex')), ',') || ';'
FROM osm_stage.points p, d WHERE p.geom && d.env
UNION ALL
SELECT 'INSERT INTO osm_stage.roads VALUES ' || string_agg(format('(%s,%L,%L,%L)', osm_id, class, tags, encode(ST_AsEWKB(geom), 'hex')), ',') || ';'
FROM osm_stage.roads r, d WHERE r.geom && d.env
UNION ALL
SELECT 'INSERT INTO osm_stage.rails VALUES ' || string_agg(format('(%s,%L,%L,%L)', osm_id, kind, tags, encode(ST_AsEWKB(geom), 'hex')), ',') || ';'
FROM osm_stage.rails r, d WHERE r.geom && d.env
UNION ALL
SELECT 'INSERT INTO osm_stage.areas VALUES ' || string_agg(format('(%L,%s,%L,%L,%L)', osm_type, osm_id, kind, tags, encode(ST_AsEWKB(geom), 'hex')), ',') || ';'
FROM osm_stage.areas a, d WHERE a.geom && d.env
UNION ALL
SELECT 'INSERT INTO osm_stage.boundaries VALUES ' || string_agg(format('(%s,%s,%L,%L)', b.osm_id, b.admin_level, b.tags,
    encode(ST_AsEWKB(CASE WHEN b.admin_level = 4 THEN ST_Multi(ST_SimplifyPreserveTopology(b.geom, 0.002)) ELSE b.geom END), 'hex')), ',') || ';'
FROM osm_stage.boundaries b
WHERE b.admin_level = 4 AND b.tags->>'name' IN ('Москва', 'Московская область')
   OR b.osm_id IN (SELECT osm_id FROM territory.admin_area
                   WHERE name = :'district'
                      OR id = (SELECT parent_id FROM territory.admin_area WHERE name = :'district'));

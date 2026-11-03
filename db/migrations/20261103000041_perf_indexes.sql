-- +goose Up

-- Точки притяжения по границам районов и округов (список территорий, профиль): поиск по
-- координатам WGS84 шёл полным просмотром таблицы.
CREATE INDEX poi_point_idx ON infra.poi USING gist (point);

-- Объекты реестра по району и округу (списки территорий, сводки).
CREATE INDEX facility_district_idx ON infra.facility (district_id) WHERE status = 'active';
CREATE INDEX facility_okrug_idx ON infra.facility (okrug_id) WHERE status = 'active';

-- +goose Down

DROP INDEX infra.facility_okrug_idx;
DROP INDEX infra.facility_district_idx;
DROP INDEX infra.poi_point_idx;

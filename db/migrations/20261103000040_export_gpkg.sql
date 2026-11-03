-- +goose Up

-- Выгрузка в GeoPackage (собирает GDAL в worker).
ALTER TABLE report.export DROP CONSTRAINT export_format_chk;
ALTER TABLE report.export ADD CONSTRAINT export_format_chk CHECK (format IN ('csv', 'xlsx', 'geojson', 'gpkg'));

-- +goose Down

DELETE FROM report.export WHERE format = 'gpkg';
ALTER TABLE report.export DROP CONSTRAINT export_format_chk;
ALTER TABLE report.export ADD CONSTRAINT export_format_chk CHECK (format IN ('csv', 'xlsx', 'geojson'));

// Package etl хранит конфигурации обработки данных: стиль импорта OpenStreetMap,
// SQL-преобразования в доменную модель (OSM, наборы портала Москвы) и многоугольники вырезки
// регионов. Файлы встраиваются в бинарник.
package etl

import "embed"

// FS — встроенные файлы обработки.
//
//go:embed osm/flex.lua osm/transform.sql regions/city.geojson regions/routing.geojson mosdata/*.sql
var FS embed.FS

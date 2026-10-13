// Package etl хранит конфигурации обработки данных: стиль импорта OpenStreetMap,
// SQL-преобразования в доменную модель (OSM, наборы портала Москвы, Росстат, transport.mos.ru,
// ДТП) и многоугольники вырезки регионов. Файлы встраиваются в бинарник.
package etl

import "embed"

// FS — встроенные файлы обработки.
//
//go:embed osm/flex.lua osm/transform.sql regions/city.geojson regions/routing.geojson mosdata/*.sql rosstat/*.sql transport/*.sql dtp/*.sql
var FS embed.FS

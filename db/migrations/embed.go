// Package migrations содержит SQL-миграции схемы базы данных.
package migrations

import "embed"

// FS — встроенные файлы миграций.
//
//go:embed *.sql
var FS embed.FS

// Package web содержит фронтенд: исходники SPA и общие стили, а также собранную статику (dist),
// которая встраивается в исполняемый файл.
package web

import "embed"

// Dist — результат сборки Vite. В разработке каталог пуст, статика отдаётся сервером Vite.
//
//go:embed all:dist
var Dist embed.FS

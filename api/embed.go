// Package api содержит спецификацию OpenAPI — источник истины для сервера и клиента.
package api

import _ "embed"

// Spec — спецификация API в формате YAML.
//
//go:embed openapi.yaml
var Spec []byte

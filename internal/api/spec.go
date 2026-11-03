package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"go.yaml.in/yaml/v3"

	spec "freightflow/api"
)

// SpecJSON отдаёт спецификацию API в JSON (GET /api/v1/openapi.json) — для страницы
// документации в системе и генераторов клиентов. Спецификация не секретна: без входа.
func SpecJSON() (http.Handler, error) {
	var doc any
	if err := yaml.Unmarshal(spec.Spec, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	var (
		once sync.Once
		body []byte
		err  error
	)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { body, err = json.Marshal(doc) })
		if err != nil {
			http.Error(w, "spec", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	}), nil
}

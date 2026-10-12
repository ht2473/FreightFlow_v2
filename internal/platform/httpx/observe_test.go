package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestObserveRecordsRoutePattern(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observe, err := Observe(mp, tracenoop.NewTracerProvider())
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /facilities/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var logged string
	capture := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			logged = Route(r.Context())
		})
	}
	// Промежуточный обработчик с WithContext между Observe и маршрутизатором — как в приложении.
	h := Chain(Routes(mux), WithRequestID(), observe, capture, AccessLog(slog.New(slog.NewTextHandler(io.Discard, nil))))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/facilities/42", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if logged != "GET /facilities/{id}" {
		t.Fatalf("route in context: %q", logged)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				route, _ := dp.Attributes.Value(attribute.Key("http.route"))
				code, _ := dp.Attributes.Value(attribute.Key("http.response.status_code"))
				if route.AsString() == "GET /facilities/{id}" && code.AsString() == "204" && dp.Count == 1 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("duration metric with route not found: %+v", rm.ScopeMetrics)
	}
}

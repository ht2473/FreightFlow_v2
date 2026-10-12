package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandlerExposesRuntimeAndOtelMetrics(t *testing.T) {
	ctx := context.Background()
	tel, err := New(ctx, Config{Service: "test", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tel.Shutdown(ctx) }()

	counter, err := tel.Meters.Meter("test").Int64Counter("ff.test.events")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 3)

	rec := httptest.NewRecorder()
	tel.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{"go_goroutines", "ff_test_events_total", `service_name="freightflow-test"`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}

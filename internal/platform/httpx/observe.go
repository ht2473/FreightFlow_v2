package httpx

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

// routeHolder переносит шаблон маршрута из ServeMux наружу: маршрутизатор получает копию
// запроса (WithContext в промежуточных обработчиках), поэтому Request.Pattern снаружи не виден.
type routeHolder struct{ pattern string }

const routeKey ctxKey = 100

// Route возвращает шаблон маршрута текущего запроса («GET /readyz»), если он уже определён.
func Route(ctx context.Context) string {
	if h, ok := ctx.Value(routeKey).(*routeHolder); ok {
		return h.pattern
	}
	return ""
}

// Routes оборачивает маршрутизатор и сообщает внешним обработчикам выбранный шаблон.
func Routes(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if h, ok := r.Context().Value(routeKey).(*routeHolder); ok && r.Pattern != "" {
			h.pattern = r.Pattern
			span := trace.SpanFromContext(r.Context())
			span.SetName(r.Pattern)
			span.SetAttributes(attribute.String("http.route", r.Pattern))
		}
	})
}

// Observe трассирует запросы и считает метрики: длительность по маршруту, методу и коду
// ответа, число запросов в обработке.
func Observe(mp metric.MeterProvider, tp trace.TracerProvider) (Middleware, error) {
	meter := mp.Meter("freightflow/httpx")
	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Длительность обработки HTTP-запроса"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
	)
	if err != nil {
		return nil, err
	}
	active, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("Запросы в обработке"))
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		measured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			holder := &routeHolder{}
			ctx := context.WithValue(r.Context(), routeKey, holder)
			rec := &statusRecorder{ResponseWriter: w}
			start := time.Now()
			// Паника внутри перехватывается Recover, поэтому учёт после вызова выполняется всегда.
			active.Add(ctx, 1)
			next.ServeHTTP(rec, r.WithContext(ctx))
			active.Add(ctx, -1)

			route := holder.pattern
			if route == "" {
				route = "unmatched"
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", route),
				attribute.String("http.response.status_code", strconv.Itoa(status)),
			))
		})
		// Метрики otelhttp отключены: свои учитывают шаблон маршрута.
		return otelhttp.NewHandler(measured, "http",
			otelhttp.WithTracerProvider(tp),
			otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }),
		)
	}, nil
}

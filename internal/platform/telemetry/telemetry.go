// Package telemetry настраивает метрики и трассировку: метрики OpenTelemetry отдаются
// в формате Prometheus на служебном адресе, трассы — по OTLP, если задан адрес приёмника.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config — параметры телеметрии процесса.
type Config struct {
	Service string // app, worker
	Version string
	// OTLPEndpoint — адрес приёмника трасс (http://localhost:4318); пусто — трассировка выключена.
	OTLPEndpoint string
	// SampleRatio — доля трассируемых запросов верхнего уровня (0..1).
	SampleRatio float64
}

// Telemetry держит провайдеры метрик и трасс процесса.
type Telemetry struct {
	registry *prometheus.Registry
	Meters   metric.MeterProvider
	Tracers  trace.TracerProvider
	shutdown []func(context.Context) error
}

// New создаёт провайдеры и делает их глобальными для библиотек, которые берут их из otel.
func New(ctx context.Context, cfg Config) (*Telemetry, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("freightflow-"+cfg.Service),
		semconv.ServiceVersion(cfg.Version),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	exporter, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithResource(res))
	t := &Telemetry{registry: reg, Meters: mp}
	t.shutdown = append(t.shutdown, mp.Shutdown)

	if cfg.OTLPEndpoint == "" {
		t.Tracers = noop.NewTracerProvider()
	} else {
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint))
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		ratio := cfg.SampleRatio
		if ratio <= 0 {
			ratio = 1
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		)
		t.Tracers = tp
		t.shutdown = append(t.shutdown, tp.Shutdown)
	}

	otel.SetMeterProvider(t.Meters)
	otel.SetTracerProvider(t.Tracers)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return t, nil
}

// Handler отдаёт метрики в формате Prometheus.
func (t *Telemetry) Handler() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// Shutdown выгружает накопленные данные и останавливает провайдеры.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range t.shutdown {
		errs = append(errs, fn(ctx))
	}
	return errors.Join(errs...)
}

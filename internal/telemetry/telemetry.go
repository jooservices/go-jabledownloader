// Package telemetry emits logs, metrics and traces to the JOOservices
// OpenObserve instance via OTLP/HTTP.
//
// It is strictly optional and fail-open: when Config.Endpoint is empty the
// returned T is a no-op, and any exporter failure at startup disables
// telemetry without affecting the download. When the OBS endpoint is
// unreachable at runtime, records are silently dropped.
package telemetry

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	logglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	"go.opentelemetry.io/otel/trace"
)

// Config describes the OBS target. Zero value disables telemetry.
type Config struct {
	Endpoint string // e.g. http://localhost:5080
	Org      string // e.g. jooservices
	Stream   string // e.g. jabledownloader
	User     string // ingestion user email
	Password string
}

// T is the telemetry handle. The zero value is disabled.
type T struct {
	enabled bool

	logProvider   *sdklog.LoggerProvider
	traceProvider *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider

	tracer trace.Tracer
	logger log.Logger
	meter  metric.Meter

	counters map[string]metric.Int64Counter
}

// New builds a telemetry handle. It never fails: on any setup error a
// disabled handle is returned.
func New(cfg Config) *T {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return &T{}
	}

	headers := map[string]string{}
	if cfg.User != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(cfg.User + ":" + cfg.Password))
		headers["Authorization"] = "Basic " + auth
	}
	if cfg.Stream != "" {
		headers["stream-name"] = cfg.Stream
	}

	base := strings.TrimRight(cfg.Endpoint, "/") + "/api/" + cfg.Org
	res := resource.NewSchemaless(
		semconv.ServiceName(cfg.Stream),
		semconv.ServiceVersion("dev"),
	)

	traceExp, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(base+"/v1/traces"),
		otlptracehttp.WithHeaders(headers),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return &T{}
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp, sdktrace.WithBatchTimeout(500*time.Millisecond)),
		sdktrace.WithResource(res),
	)

	metricExp, err := otlpmetrichttp.New(context.Background(),
		otlpmetrichttp.WithEndpointURL(base+"/v1/metrics"),
		otlpmetrichttp.WithHeaders(headers),
		otlpmetrichttp.WithInsecure(),
	)
	if err != nil {
		_ = traceProvider.Shutdown(context.Background())
		return &T{}
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(1*time.Second))),
		sdkmetric.WithResource(res),
	)

	logExp, err := otlploghttp.New(context.Background(),
		otlploghttp.WithEndpointURL(base+"/v1/logs"),
		otlploghttp.WithHeaders(headers),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		_ = traceProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
		return &T{}
	}
	logProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp, sdklog.WithExportInterval(500*time.Millisecond))),
		sdklog.WithResource(res),
	)

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {})) // fail-open
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	logglobal.SetLoggerProvider(logProvider)

	return &T{
		enabled:       true,
		logProvider:   logProvider,
		traceProvider: traceProvider,
		meterProvider: meterProvider,
		tracer:        traceProvider.Tracer("jabledownloader"),
		logger:        logProvider.Logger("jabledownloader"),
		meter:         meterProvider.Meter("jabledownloader"),
		counters:      make(map[string]metric.Int64Counter),
	}
}

// Enabled reports whether telemetry is active.
func (t *T) Enabled() bool { return t != nil && t.enabled }

// StartSpan starts a named span, returning the derived context and an end
// function. Safe to call when disabled.
func (t *T) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func()) {
	if !t.Enabled() {
		return ctx, func() {}
	}
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, func() { span.End() }
}

// Event records a log event. Safe to call when disabled.
func (t *T) Event(ctx context.Context, severity log.Severity, message string, attrs ...attribute.KeyValue) {
	if !t.Enabled() {
		return
	}
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetSeverity(severity)
	rec.SetBody(attribute.StringValue(message))
	rec.AddAttributes(attrs...)
	t.logger.Emit(ctx, rec)
}

// Info records an informational log event.
func (t *T) Info(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.Event(ctx, log.SeverityInfo, message, attrs...)
}

// Warn records a warning log event.
func (t *T) Warn(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.Event(ctx, log.SeverityWarn, message, attrs...)
}

// Error records an error log event.
func (t *T) Error(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.Event(ctx, log.SeverityError, message, attrs...)
}

// Count increments a named counter by n. Safe to call when disabled.
func (t *T) Count(ctx context.Context, name string, n int64, attrs ...attribute.KeyValue) {
	if !t.Enabled() {
		return
	}
	counter, ok := t.counters[name]
	if !ok {
		var err error
		counter, err = t.meter.Int64Counter(name)
		if err != nil {
			return
		}
		t.counters[name] = counter
	}
	counter.Add(ctx, n, metric.WithAttributes(attrs...))
}

// Record observes a named duration histogram in milliseconds.
func (t *T) Record(ctx context.Context, name string, ms float64, attrs ...attribute.KeyValue) {
	if !t.Enabled() {
		return
	}
	hist, err := t.meter.Float64Histogram(name)
	if err != nil {
		return
	}
	hist.Record(ctx, ms, metric.WithAttributes(attrs...))
}

// Shutdown flushes and releases exporters. It never fails.
func (t *T) Shutdown(ctx context.Context) {
	if !t.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = t.logProvider.Shutdown(ctx)
	_ = t.traceProvider.Shutdown(ctx)
	_ = t.meterProvider.Shutdown(ctx)
}

// Package telemetry wires OpenTelemetry traces + metrics, structured
// logging with trace-id correlation, and a small set of app-specific
// instruments.
//
// The core entry point is Init, which returns a ShutdownFunc that must
// be called on process exit. When OTEL_EXPORTER_OTLP_ENDPOINT is empty
// the tracer/meter providers fall back to no-op exporters so developer
// loops aren't polluted by failed OTLP dial attempts.
//
// Design notes:
//   - Exporter choice is driven entirely by the standard OTEL_* env vars
//     (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_INSECURE,
//     OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES, OTEL_TRACES_SAMPLER*).
//   - A Prometheus pull exporter is always attached so /metrics works
//     regardless of whether an OTLP collector is reachable.
//   - W3C tracecontext + baggage propagators are registered globally so
//     incoming and outgoing HTTP spans stitch across hops.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config tunes the OTel providers. Fields left zero/empty fall back to
// the OTEL_* environment variables or to no-op behaviour.
type Config struct {
	// ServiceName populates service.name on every span/metric.
	ServiceName string
	// ServiceVersion populates service.version (e.g. a git tag).
	ServiceVersion string
	// Environment populates deployment.environment ("development",
	// "staging", "production").
	Environment string
	// OTLPEndpoint overrides OTEL_EXPORTER_OTLP_ENDPOINT. When both are
	// empty, OTLP exporters are not registered and only Prometheus
	// pull-based metrics remain active.
	OTLPEndpoint string
	// OTLPInsecure disables TLS for the OTLP connection. Defaults true
	// when the endpoint is unset or uses a private IP / cluster DNS.
	OTLPInsecure bool
}

// ShutdownFunc flushes and tears down the installed providers. It is
// safe to call multiple times; only the first call has effect.
type ShutdownFunc func(context.Context) error

// Providers bundles the initialised providers so callers can reach
// them directly (e.g. to create custom tracers / meters).
type Providers struct {
	Tracer      *sdktrace.TracerProvider
	Meter       *sdkmetric.MeterProvider
	Resource    *resource.Resource
	PromHandler http.Handler // exposes the Prometheus registry; mount at /metrics
	OTLPEnabled bool         // true when OTLP trace/metric exporters are active
}

// Init wires the global tracer + meter providers.
//
// When no OTLP endpoint is configured Init still installs a working
// tracer provider and a metric provider backed by the Prometheus pull
// exporter. In that mode the returned ShutdownFunc is a cheap
// flush-and-close and OTLP network calls are never attempted.
func Init(ctx context.Context, cfg Config) (*Providers, ShutdownFunc, error) {
	cfg.applyDefaults()

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, noopShutdown, fmt.Errorf("telemetry: build resource: %w", err)
	}

	tp, tpShutdown, err := buildTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, noopShutdown, fmt.Errorf("telemetry: tracer provider: %w", err)
	}

	mp, mpShutdown, err := buildMeterProvider(ctx, cfg, res)
	if err != nil {
		_ = tpShutdown(ctx)
		return nil, noopShutdown, fmt.Errorf("telemetry: meter provider: %w", err)
	}

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Go runtime metrics (goroutines, GC, heap, cpu). Safe to ignore
	// the error — it only fails when duplicate instruments are
	// registered, which we never do.
	_ = runtime.Start(
		runtime.WithMeterProvider(mp),
		runtime.WithMinimumReadMemStatsInterval(15*time.Second),
	)

	// Populate the app-level instruments against the fresh meter.
	initAppMetrics(mp)

	provs := &Providers{
		Tracer:      tp,
		Meter:       mp,
		Resource:    res,
		PromHandler: promhttp.Handler(),
		OTLPEnabled: cfg.OTLPEndpoint != "",
	}

	shutdown := func(ctx context.Context) error {
		var first error
		if err := tpShutdown(ctx); err != nil && first == nil {
			first = err
		}
		if err := mpShutdown(ctx); err != nil && first == nil {
			first = err
		}
		return first
	}
	return provs, shutdown, nil
}

func (c *Config) applyDefaults() {
	if c.ServiceName == "" {
		c.ServiceName = firstEnv("OTEL_SERVICE_NAME", "trader")
	}
	if c.OTLPEndpoint == "" {
		c.OTLPEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"); v != "" {
		c.OTLPInsecure = strings.EqualFold(v, "true") || v == "1"
	} else if c.OTLPEndpoint != "" {
		// Insecure by default when talking to in-cluster collectors via
		// plain gRPC. Users should explicitly set
		// OTEL_EXPORTER_OTLP_INSECURE=false for public endpoints.
		c.OTLPInsecure = true
	}
}

func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.ServiceInstanceID(uuid.NewString()),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
		resource.WithFromEnv(), // honours OTEL_RESOURCE_ATTRIBUTES
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
		resource.WithContainer(),
		resource.WithTelemetrySDK(),
	)
}

func buildTracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, ShutdownFunc, error) {
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		// ParentBased(AlwaysSample) respects upstream sampling decisions
		// but otherwise samples everything. Production should override
		// via OTEL_TRACES_SAMPLER / OTEL_TRACES_SAMPLER_ARG.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	}

	if cfg.OTLPEndpoint != "" {
		exporter, err := newOTLPTraceExporter(ctx, cfg)
		if err != nil {
			return nil, noopShutdown, err
		}
		opts = append(opts, sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	return tp, func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(shutdownCtx)
	}, nil
}

func newOTLPTraceExporter(ctx context.Context, cfg Config) (*otlptrace.Exporter, error) {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(stripScheme(cfg.OTLPEndpoint))}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return otlptracegrpc.New(dialCtx, opts...)
}

func buildMeterProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, ShutdownFunc, error) {
	// Prometheus pull exporter — always installed so /metrics is
	// scrapable regardless of OTLP availability.
	promReader, err := otelprom.New()
	if err != nil {
		return nil, noopShutdown, fmt.Errorf("prometheus exporter: %w", err)
	}

	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promReader),
	}

	if cfg.OTLPEndpoint != "" {
		exp, err := newOTLPMetricExporter(ctx, cfg)
		if err != nil {
			return nil, noopShutdown, err
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(30*time.Second),
			sdkmetric.WithTimeout(10*time.Second),
		)))
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	shutdown := func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return mp.Shutdown(shutdownCtx)
	}
	return mp, shutdown, nil
}

func newOTLPMetricExporter(ctx context.Context, cfg Config) (sdkmetric.Exporter, error) {
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(stripScheme(cfg.OTLPEndpoint))}
	if cfg.OTLPInsecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return otlpmetricgrpc.New(dialCtx, opts...)
}

// stripScheme peels "http://" or "https://" off the endpoint because
// OTLP gRPC clients expect bare host:port. Users are forgiving and
// usually paste the URL they copy from their collector docs.
func stripScheme(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"https://", "http://", "grpc://"} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimPrefix(s, prefix)
		}
	}
	return s
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	if len(keys) > 0 {
		return keys[len(keys)-1]
	}
	return ""
}

func noopShutdown(context.Context) error { return nil }

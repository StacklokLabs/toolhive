// Package telemetry provides OpenTelemetry instrumentation for ToolHive MCP server proxies.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/stacklok/toolhive/pkg/kubernetes/transport/types"
	"github.com/stacklok/toolhive/pkg/kubernetes/versions"
)

// Config holds the configuration for OpenTelemetry instrumentation.
type Config struct {
	// Endpoint is the OTLP endpoint URL
	Endpoint string

	// ServiceName is the service name for telemetry
	ServiceName string

	// ServiceVersion is the service version for telemetry
	ServiceVersion string

	// SamplingRate is the trace sampling rate (0.0-1.0)
	SamplingRate float64

	// Headers contains authentication headers for the OTLP endpoint
	Headers map[string]string

	// Insecure indicates whether to disable TLS verification
	Insecure bool

	// EnablePrometheusMetricsPath controls whether to expose Prometheus-style /metrics endpoint
	// The metrics are served on the main transport port at /metrics
	// This is separate from OTLP metrics which are sent to the Endpoint
	EnablePrometheusMetricsPath bool

	// EnvironmentVariables is a list of environment variable names that should be
	// included in telemetry spans as attributes. Only variables in this list will
	// be read from the host machine and included in spans for observability.
	// Example: []string{"NODE_ENV", "DEPLOYMENT_ENV", "SERVICE_VERSION"}
	EnvironmentVariables []string
}

// DefaultConfig returns a default telemetry configuration.
func DefaultConfig() Config {
	versionInfo := versions.GetVersionInfo()
	return Config{
		ServiceName:                 "toolhive-mcp-proxy",
		ServiceVersion:              versionInfo.Version,
		SamplingRate:                0.1, // 10% sampling by default
		Headers:                     make(map[string]string),
		Insecure:                    false,
		EnablePrometheusMetricsPath: false,      // No metrics endpoint by default
		EnvironmentVariables:        []string{}, // No environment variables by default
	}
}

// Provider encapsulates OpenTelemetry providers and configuration.
type Provider struct {
	config            Config
	tracerProvider    trace.TracerProvider
	meterProvider     metric.MeterProvider
	prometheusHandler http.Handler
	shutdown          func(context.Context) error
}

// NewProvider creates a new OpenTelemetry provider with the given configuration.
func NewProvider(ctx context.Context, config Config) (*Provider, error) {
	if config.Endpoint == "" && !config.EnablePrometheusMetricsPath {
		return createNoOpProvider(config), nil
	}

	res, err := createResource(ctx, config)
	if err != nil {
		return nil, err
	}

	tracerProvider, err := createTracerProvider(ctx, config, res)
	if err != nil {
		return nil, err
	}

	meterProvider, prometheusHandler, err := createMeterProvider(ctx, config, res)
	if err != nil {
		return nil, err
	}

	setGlobalProviders(tracerProvider, meterProvider)

	shutdown := createShutdownFunc(tracerProvider, meterProvider)

	return &Provider{
		config:            config,
		tracerProvider:    tracerProvider,
		meterProvider:     meterProvider,
		prometheusHandler: prometheusHandler,
		shutdown:          shutdown,
	}, nil
}

// createNoOpProvider creates a no-op provider when no telemetry is configured.
func createNoOpProvider(config Config) *Provider {
	return &Provider{
		config:            config,
		tracerProvider:    tracenoop.NewTracerProvider(),
		meterProvider:     noop.NewMeterProvider(),
		prometheusHandler: nil,
		shutdown:          func(context.Context) error { return nil },
	}
}

// createResource creates an OpenTelemetry resource with service information.
func createResource(ctx context.Context, config Config) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}
	return res, nil
}

// createTracerProvider creates a tracer provider based on configuration.
func createTracerProvider(ctx context.Context, config Config, res *resource.Resource) (trace.TracerProvider, error) {
	if config.Endpoint == "" {
		return tracenoop.NewTracerProvider(), nil
	}

	traceExporter, err := createTraceExporter(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	sampler := sdktrace.TraceIDRatioBased(config.SamplingRate)
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	), nil
}

// createMeterProvider creates a meter provider and Prometheus handler based on configuration.
func createMeterProvider(ctx context.Context, config Config, res *resource.Resource) (metric.MeterProvider, http.Handler, error) {
	var readers []sdkmetric.Reader
	var prometheusHandler http.Handler

	// Add OTLP metric reader if endpoint is configured
	if config.Endpoint != "" {
		metricExporter, err := createMetricExporter(ctx, config)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create metric exporter: %w", err)
		}
		readers = append(readers, sdkmetric.NewPeriodicReader(metricExporter))
	}

	// Add Prometheus reader if Prometheus metrics path is enabled
	if config.EnablePrometheusMetricsPath {
		// Create a dedicated registry for this provider to avoid conflicts
		registry := promclient.NewRegistry()

		// Add standard Go runtime metrics (best practice for production)
		registry.MustRegister(collectors.NewGoCollector())
		registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

		prometheusExporter, err := prometheus.New(prometheus.WithRegisterer(registry))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create prometheus exporter: %w", err)
		}
		readers = append(readers, prometheusExporter)

		// Create handler with proper error handling options
		prometheusHandler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			ErrorHandling: promhttp.ContinueOnError,
			ErrorLog:      nil, // Use default logger
		})
	}

	// Create meter provider with configured readers
	if len(readers) > 0 {
		opts := []sdkmetric.Option{sdkmetric.WithResource(res)}
		for _, reader := range readers {
			opts = append(opts, sdkmetric.WithReader(reader))
		}
		return sdkmetric.NewMeterProvider(opts...), prometheusHandler, nil
	}

	return noop.NewMeterProvider(), prometheusHandler, nil
}

// setGlobalProviders sets the global OpenTelemetry providers.
func setGlobalProviders(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) {
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// createShutdownFunc creates a shutdown function for the providers.
func createShutdownFunc(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) func(context.Context) error {
	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Shutdown meter provider if it's an SDK provider
		if sdkMeterProvider, ok := meterProvider.(*sdkmetric.MeterProvider); ok {
			if err := sdkMeterProvider.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("failed to shutdown meter provider: %w", err)
			}
		}

		// Shutdown tracer provider if it's an SDK provider
		if sdkTracerProvider, ok := tracerProvider.(*sdktrace.TracerProvider); ok {
			if err := sdkTracerProvider.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("failed to shutdown tracer provider: %w", err)
			}
		}

		return nil
	}
}

// createTraceExporter creates an OTLP trace exporter based on the configuration.
func createTraceExporter(ctx context.Context, config Config) (sdktrace.SpanExporter, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("OTLP endpoint is required when telemetry is enabled")
	}

	// Prepare options for OTLP HTTP exporter
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.Endpoint),
	}

	// Add headers if provided
	if len(config.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(config.Headers))
	}

	// Configure TLS
	if config.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	return otlptracehttp.New(ctx, opts...)
}

// createMetricExporter creates an OTLP metric exporter based on the configuration.
func createMetricExporter(ctx context.Context, config Config) (sdkmetric.Exporter, error) {
	// Prepare options for OTLP HTTP exporter
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(config.Endpoint),
	}

	// Add headers if provided
	if len(config.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(config.Headers))
	}

	// Configure TLS
	if config.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	return otlpmetrichttp.New(ctx, opts...)
}

// Middleware returns an HTTP middleware that instruments requests with OpenTelemetry.
// serverName is the name of the MCP server (e.g., "github", "fetch")
// transport is the backend transport type ("stdio" or "sse")
func (p *Provider) Middleware(serverName, transport string) types.Middleware {
	return NewHTTPMiddleware(p.config, p.tracerProvider, p.meterProvider, serverName, transport)
}

// Shutdown gracefully shuts down the telemetry provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.shutdown != nil {
		return p.shutdown(ctx)
	}
	return nil
}

// TracerProvider returns the configured tracer provider.
func (p *Provider) TracerProvider() trace.TracerProvider {
	return p.tracerProvider
}

// MeterProvider returns the configured meter provider.
func (p *Provider) MeterProvider() metric.MeterProvider {
	return p.meterProvider
}

// PrometheusHandler returns the Prometheus metrics handler if configured.
// Returns nil if no metrics port is configured.
func (p *Provider) PrometheusHandler() http.Handler {
	return p.prometheusHandler
}

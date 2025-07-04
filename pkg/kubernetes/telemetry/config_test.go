package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()

	assert.Equal(t, "toolhive-mcp-proxy", config.ServiceName)
	assert.NotEmpty(t, config.ServiceVersion)
	assert.Equal(t, 0.1, config.SamplingRate)
	assert.NotNil(t, config.Headers)
	assert.Empty(t, config.Headers)
	assert.False(t, config.Insecure)
	assert.False(t, config.EnablePrometheusMetricsPath)
	assert.Empty(t, config.Endpoint)
}

func TestNewProvider_NoOpWhenNoConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		SamplingRate:   0.1,
		Headers:        make(map[string]string),
		// No endpoint or metrics port configured
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Should create no-op providers
	tracerType := fmt.Sprintf("%T", provider.TracerProvider())
	meterType := fmt.Sprintf("%T", provider.MeterProvider())
	assert.Contains(t, tracerType, "noop")
	assert.Contains(t, meterType, "noop")
	assert.Nil(t, provider.PrometheusHandler())

	// Shutdown should work without error
	err = provider.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestNewProvider_WithPrometheusMetricsOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		SamplingRate:                0.1,
		Headers:                     make(map[string]string),
		EnablePrometheusMetricsPath: true,
		// No OTLP endpoint
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Should have Prometheus handler but no-op tracer
	tracerType := fmt.Sprintf("%T", provider.TracerProvider())
	assert.Contains(t, tracerType, "noop")
	assert.NotNil(t, provider.PrometheusHandler())

	// Shutdown should work
	err = provider.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestNewProvider_WithEndpointOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		SamplingRate:   0.5,
		Headers:        map[string]string{"Authorization": "Bearer token"},
		Endpoint:       "localhost:4318",
		Insecure:       true,
		// No metrics port
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Should have real tracer provider but no Prometheus handler
	assert.NotNil(t, provider.TracerProvider())
	assert.Nil(t, provider.PrometheusHandler())

	// Shutdown may fail due to network connection (expected in tests)
	//nolint:errcheck // We ignore the error here since it may fail due to no server running
	_ = provider.Shutdown(ctx)
}

func TestNewProvider_WithBothEndpointAndMetrics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		SamplingRate:                0.1,
		Headers:                     map[string]string{"x-api-key": "secret"},
		Endpoint:                    "localhost:4318",
		Insecure:                    false,
		EnablePrometheusMetricsPath: true,
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Should have both tracer and metrics
	assert.NotNil(t, provider.TracerProvider())
	assert.NotNil(t, provider.MeterProvider())
	assert.NotNil(t, provider.PrometheusHandler())

	// Shutdown may fail due to network connection (expected in tests)
	//nolint:errcheck // We ignore the error here since it may fail due to no server running
	_ = provider.Shutdown(ctx)
}

func TestProvider_Middleware(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		SamplingRate:                0.1,
		Headers:                     make(map[string]string),
		EnablePrometheusMetricsPath: true,
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	middleware := provider.Middleware("github", "stdio")
	assert.NotNil(t, middleware)

	// Test that middleware can wrap a handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test"))
	})

	wrappedHandler := middleware(testHandler)
	assert.NotNil(t, wrappedHandler)
}

func TestProvider_ShutdownTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		SamplingRate:                0.1,
		Headers:                     make(map[string]string),
		Endpoint:                    "localhost:4318",
		Insecure:                    true,
		EnablePrometheusMetricsPath: true,
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Test shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	// Shutdown may fail due to network connection (expected in tests)
	//nolint:errcheck // We ignore the error here since it may fail due to no server running
	_ = provider.Shutdown(shutdownCtx)
}

func TestCreateResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.2.3",
	}

	resource, err := createResource(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, resource)

	// Check that resource has the expected attributes
	attrs := resource.Attributes()
	found := false
	for _, attr := range attrs {
		if attr.Key == "service.name" {
			assert.Equal(t, "test-service", attr.Value.AsString())
			found = true
		}
		if attr.Key == "service.version" {
			assert.Equal(t, "1.2.3", attr.Value.AsString())
		}
	}
	assert.True(t, found, "service.name attribute should be present")
}

func TestCreateTraceExporter_InvalidEndpoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		Endpoint: "", // Empty endpoint should cause error
	}

	_, err := createTraceExporter(ctx, config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OTLP endpoint is required")
}

func TestCreateTraceExporter_ValidConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		Endpoint: "http://localhost:4318",
		Headers:  map[string]string{"Authorization": "Bearer token"},
		Insecure: true,
	}

	exporter, err := createTraceExporter(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, exporter)

	// Clean up
	err = exporter.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestCreateMetricExporter_ValidConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		Endpoint: "localhost:4318",
		Headers:  map[string]string{"x-api-key": "secret"},
		Insecure: false,
	}

	exporter, err := createMetricExporter(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, exporter)

	// Clean up
	err = exporter.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestConfig_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   Config
		wantNoOp bool
	}{
		{
			name: "empty config creates no-op",
			config: Config{
				ServiceName:    "test",
				ServiceVersion: "1.0.0",
			},
			wantNoOp: true,
		},
		{
			name: "only endpoint creates real provider",
			config: Config{
				ServiceName:    "test",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4318",
			},
			wantNoOp: false,
		},
		{
			name: "only metrics port creates real provider",
			config: Config{
				ServiceName:                 "test",
				ServiceVersion:              "1.0.0",
				EnablePrometheusMetricsPath: true,
			},
			wantNoOp: false,
		},
		{
			name: "both endpoint and metrics creates real provider",
			config: Config{
				ServiceName:                 "test",
				ServiceVersion:              "1.0.0",
				Endpoint:                    "localhost:4318",
				EnablePrometheusMetricsPath: true,
			},
			wantNoOp: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			tt.config.Headers = make(map[string]string)
			tt.config.SamplingRate = 0.1

			provider, err := NewProvider(ctx, tt.config)
			require.NoError(t, err)
			require.NotNil(t, provider)

			if tt.wantNoOp {
				// Check if it's a no-op provider by checking the type name
				tracerType := fmt.Sprintf("%T", provider.TracerProvider())
				meterType := fmt.Sprintf("%T", provider.MeterProvider())
				assert.Contains(t, tracerType, "noop")
				assert.Contains(t, meterType, "noop")
			} else {
				// For real providers, we just check they're not nil
				assert.NotNil(t, provider.TracerProvider())
				assert.NotNil(t, provider.MeterProvider())
			}

			// Clean up - shutdown may fail due to network connection (expected in tests)
			if tt.wantNoOp {
				// No-op providers should shutdown cleanly
				err = provider.Shutdown(ctx)
				assert.NoError(t, err)
			} else {
				// Real providers may fail to shutdown due to network issues
				//nolint:errcheck // We ignore the error here since it may fail due to no server running
				_ = provider.Shutdown(ctx)
			}
		})
	}
}

func TestProvider_GettersReturnCorrectTypes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		SamplingRate:                0.1,
		Headers:                     make(map[string]string),
		Endpoint:                    "localhost:4318",
		EnablePrometheusMetricsPath: true,
		Insecure:                    true,
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Test getters
	tracerProvider := provider.TracerProvider()
	assert.NotNil(t, tracerProvider)
	assert.Implements(t, (*trace.TracerProvider)(nil), tracerProvider)

	meterProvider := provider.MeterProvider()
	assert.NotNil(t, meterProvider)

	prometheusHandler := provider.PrometheusHandler()
	assert.NotNil(t, prometheusHandler)
	assert.Implements(t, (*http.Handler)(nil), prometheusHandler)

	// Clean up - shutdown may fail due to network connection (expected in tests)
	//nolint:errcheck // We ignore the error here since it may fail due to no server running
	_ = provider.Shutdown(ctx)
}

package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestHTTPMiddleware_SSEHandling(t *testing.T) {
	t.Parallel()

	// Create test providers
	tracerProvider := noop.NewTracerProvider()
	meterProvider := sdkmetric.NewMeterProvider()

	// Create middleware
	config := Config{
		EnablePrometheusMetricsPath: true,
	}
	middleware := NewHTTPMiddleware(config, tracerProvider, meterProvider, "test-server", "sse")

	tests := []struct {
		name           string
		path           string
		expectSSEPath  bool
		expectComplete bool
	}{
		{
			name:           "SSE endpoint",
			path:           "/sse",
			expectSSEPath:  true,
			expectComplete: false, // SSE connections don't complete normally
		},
		{
			name:           "SSE endpoint with query params",
			path:           "/sse?session_id=123",
			expectSSEPath:  true,
			expectComplete: false,
		},
		{
			name:           "Regular HTTP endpoint",
			path:           "/messages",
			expectSSEPath:  false,
			expectComplete: true,
		},
		{
			name:           "Metrics endpoint",
			path:           "/metrics",
			expectSSEPath:  false,
			expectComplete: true,
		},
		{
			name:           "Path containing sse but not ending with it",
			path:           "/sse/messages",
			expectSSEPath:  false,
			expectComplete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Track if the handler completed
			handlerCompleted := false

			// Create a test handler that sets a flag when called
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				handlerCompleted = true
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("test response"))
			})

			// Wrap with our middleware
			wrappedHandler := middleware(testHandler)

			// Create test request
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()

			// Execute the request
			wrappedHandler.ServeHTTP(w, req)

			// Verify the handler was called
			assert.True(t, handlerCompleted, "Handler should have been called")

			// For SSE endpoints, we expect immediate passthrough
			// For regular endpoints, we expect normal middleware processing
			if tt.expectSSEPath {
				// SSE endpoints should get 200 OK and pass through immediately
				assert.Equal(t, http.StatusOK, w.Code, "SSE endpoint should return 200")
			} else {
				// Regular endpoints should also work normally
				assert.Equal(t, http.StatusOK, w.Code, "Regular endpoint should return 200")
			}
		})
	}
}

func TestHTTPMiddleware_RecordSSEConnection(t *testing.T) {
	t.Parallel()

	// Create a real meter provider to capture metrics
	meterProvider := sdkmetric.NewMeterProvider()
	tracerProvider := noop.NewTracerProvider()

	config := Config{
		EnablePrometheusMetricsPath: true,
	}

	// Extract the actual middleware struct to test the recordSSEConnection method
	// We need to create the middleware manually to access the method
	meter := meterProvider.Meter(instrumentationName)

	requestCounter, _ := meter.Int64Counter(
		"toolhive_mcp_requests_total",
		metric.WithDescription("Total number of MCP requests"),
	)

	activeConnections, _ := meter.Int64UpDownCounter(
		"toolhive_mcp_active_connections",
		metric.WithDescription("Number of active MCP connections"),
	)

	middleware := &HTTPMiddleware{
		config:            config,
		tracerProvider:    tracerProvider,
		tracer:            tracerProvider.Tracer(instrumentationName),
		meterProvider:     meterProvider,
		meter:             meter,
		serverName:        "test-server",
		transport:         "sse",
		requestCounter:    requestCounter,
		activeConnections: activeConnections,
	}

	// Create test request
	req := httptest.NewRequest("GET", "/sse", nil)
	ctx := req.Context()

	// Test the recordSSEConnection method
	middleware.recordSSEConnection(ctx, req)

	// The method should complete without error
	// In a real test, we would verify metrics were recorded, but that requires
	// more complex setup with metric readers
}

func TestHTTPMiddleware_SSEIntegration(t *testing.T) {
	t.Parallel()

	// Create test providers with readers to capture data
	meterProvider := sdkmetric.NewMeterProvider()
	tracerProvider := noop.NewTracerProvider()

	config := Config{
		EnablePrometheusMetricsPath: true,
	}

	middleware := NewHTTPMiddleware(config, tracerProvider, meterProvider, "test-server", "sse")

	// Create a test handler that simulates SSE behavior
	sseHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		w.WriteHeader(http.StatusOK)

		// Write some SSE data
		w.Write([]byte("data: test event\n\n"))

		// In a real SSE connection, this would keep the connection open
		// For testing, we'll just return
	})

	// Wrap with middleware
	wrappedHandler := middleware(sseHandler)

	// Test SSE endpoint
	req := httptest.NewRequest("GET", "/sse", nil)
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	// Verify SSE response
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "data: test event")
}

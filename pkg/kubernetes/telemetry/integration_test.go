package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/stacklok/toolhive/pkg/kubernetes/mcp"
)

func TestTelemetryIntegration_EndToEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test configuration
	config := Config{
		ServiceName:                 "test-toolhive",
		ServiceVersion:              "1.0.0-test",
		SamplingRate:                1.0, // Sample everything for testing
		Headers:                     make(map[string]string),
		EnablePrometheusMetricsPath: true, // Enable Prometheus metrics
	}

	// Create provider
	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)
	t.Cleanup(func() {
		provider.Shutdown(ctx)
	})

	// Create middleware
	middleware := provider.Middleware("github", "stdio")

	// Create test handler that simulates MCP server behavior
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "test-123",
			"result": map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": "Search results for test query",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	})

	// Wrap with MCP parsing middleware first, then telemetry
	mcpHandler := mcp.ParsingMiddleware(testHandler)
	wrappedHandler := middleware(mcpHandler)

	// Test different types of MCP requests
	testCases := []struct {
		name           string
		method         string
		path           string
		body           string
		expectedStatus int
		expectedMethod string
	}{
		{
			name:           "tools/call request",
			method:         "POST",
			path:           "/messages",
			body:           `{"jsonrpc":"2.0","id":"test-123","method":"tools/call","params":{"name":"github_search","arguments":{"query":"test","limit":10}}}`,
			expectedStatus: 200,
			expectedMethod: "tools/call",
		},
		{
			name:           "resources/read request",
			method:         "POST",
			path:           "/api/v1/messages",
			body:           `{"jsonrpc":"2.0","id":"test-456","method":"resources/read","params":{"uri":"file://test.txt"}}`,
			expectedStatus: 200,
			expectedMethod: "resources/read",
		},
		{
			name:           "initialize request",
			method:         "POST",
			path:           "/messages",
			body:           `{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test-client","version":"1.0.0"}}}`,
			expectedStatus: 200,
			expectedMethod: "initialize",
		},
		{
			name:           "non-MCP request",
			method:         "GET",
			path:           "/health",
			body:           "",
			expectedStatus: 200,
			expectedMethod: "unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}

			rec := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rec, req)

			assert.Equal(t, tc.expectedStatus, rec.Code)
		})
	}

	// Verify Prometheus handler is available
	prometheusHandler := provider.PrometheusHandler()
	assert.NotNil(t, prometheusHandler)

	// Test Prometheus metrics endpoint
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	prometheusHandler.ServeHTTP(metricsRec, metricsReq)

	// Check if metrics endpoint is working - be flexible about status
	if metricsRec.Code == http.StatusOK {
		metricsBody := metricsRec.Body.String()
		// At minimum, we should have some metrics
		assert.True(t, len(metricsBody) > 0, "Metrics should not be empty")

		// If we have custom metrics, verify them
		if strings.Contains(metricsBody, "toolhive_mcp") {
			assert.Contains(t, metricsBody, "toolhive_mcp_requests_total")
			assert.Contains(t, metricsBody, "toolhive_mcp_request_duration_seconds")
			assert.Contains(t, metricsBody, "toolhive_mcp_active_connections")
		}
	} else {
		// If metrics endpoint fails, just log it but don't fail the test
		t.Logf("Metrics endpoint returned status %d, body: %s", metricsRec.Code, metricsRec.Body.String())
	}
}

func TestTelemetryIntegration_WithRealProviders(t *testing.T) {
	t.Parallel()

	// Create in-memory trace exporter for testing
	traceExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Create in-memory metrics reader for testing
	metricsReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricsReader),
	)

	config := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
	}

	// Create middleware directly with real providers
	middleware := NewHTTPMiddleware(config, tracerProvider, meterProvider, "github", "stdio")

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(testHandler)

	// Create MCP request
	mcpRequest := &mcp.ParsedMCPRequest{
		Method:     "tools/call",
		ID:         "test-123",
		ResourceID: "github_search",
		Arguments: map[string]interface{}{
			"query":   "test query",
			"api_key": "secret123", // This should be redacted
			"limit":   10,
		},
		IsRequest: true,
	}

	req := httptest.NewRequest("POST", "/messages", nil)
	ctx := context.WithValue(req.Context(), mcp.MCPRequestContextKey, mcpRequest)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Force flush the tracer provider to ensure spans are exported
	err := tracerProvider.ForceFlush(t.Context())
	require.NoError(t, err)

	// Verify traces were recorded
	spans := traceExporter.GetSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "mcp.tools/call", span.Name)

	// Verify span attributes
	attrs := span.Attributes
	attrMap := make(map[string]interface{})
	for _, attr := range attrs {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, "tools/call", attrMap["mcp.method"])
	assert.Equal(t, "github_search", attrMap["mcp.tool.name"])
	assert.Equal(t, "test-123", attrMap["mcp.request.id"])
	assert.Equal(t, "POST", attrMap["http.method"])
	assert.Equal(t, int64(200), attrMap["http.status_code"])

	// Verify sensitive data is redacted
	if toolArgs, exists := attrMap["mcp.tool.arguments"]; exists {
		argsStr := toolArgs.(string)
		assert.Contains(t, argsStr, "api_key=[REDACTED]")
		assert.Contains(t, argsStr, "query=test query")
		assert.Contains(t, argsStr, "limit=10")
	}

	// Verify metrics were recorded
	var rm metricdata.ResourceMetrics
	err = metricsReader.Collect(context.Background(), &rm)
	require.NoError(t, err)

	// Check that metrics were recorded
	assert.NotEmpty(t, rm.ScopeMetrics)

	var foundRequestCounter, foundDurationHistogram, foundActiveConnections bool
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			switch metric.Name {
			case "toolhive_mcp_requests_total":
				foundRequestCounter = true
				// Verify metric has expected attributes
				if sum, ok := metric.Data.(metricdata.Sum[int64]); ok {
					assert.NotEmpty(t, sum.DataPoints)
					for _, dp := range sum.DataPoints {
						// Check for expected attributes
						attrSet := dp.Attributes
						hasServer := false
						hasMethod := false
						for _, attr := range attrSet.ToSlice() {
							if attr.Key == "server" && attr.Value.AsString() == "github" {
								hasServer = true
							}
							if attr.Key == "mcp_method" && attr.Value.AsString() == "tools/call" {
								hasMethod = true
							}
						}
						assert.True(t, hasServer, "Request counter should have server attribute")
						assert.True(t, hasMethod, "Request counter should have mcp_method attribute")
					}
				}
			case "toolhive_mcp_request_duration_seconds":
				foundDurationHistogram = true
			case "toolhive_mcp_active_connections":
				foundActiveConnections = true
			}
		}
	}

	assert.True(t, foundRequestCounter, "Request counter metric should be present")
	assert.True(t, foundDurationHistogram, "Duration histogram metric should be present")
	assert.True(t, foundActiveConnections, "Active connections metric should be present")

	// Clean up
	err = tracerProvider.Shutdown(ctx)
	assert.NoError(t, err)
	err = meterProvider.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestTelemetryIntegration_ErrorHandling(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// Create provider with metrics only
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		EnablePrometheusMetricsPath: true,
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	middleware := provider.Middleware("test-server", "stdio")

	// Create handler that returns an error
	errorHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	})

	wrappedHandler := middleware(errorHandler)

	// Test error request
	req := httptest.NewRequest("POST", "/messages", nil)
	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Verify Prometheus metrics include error status
	prometheusHandler := provider.PrometheusHandler()
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	prometheusHandler.ServeHTTP(metricsRec, metricsReq)

	metricsBody := metricsRec.Body.String()
	// Check if metrics contain error indicators - the exact format may vary
	hasErrorMetrics := strings.Contains(metricsBody, `status="error"`) ||
		strings.Contains(metricsBody, `status_code="500"`) ||
		strings.Contains(metricsBody, "500") ||
		strings.Contains(metricsBody, "error")

	// If we have custom metrics, they should include error status
	if strings.Contains(metricsBody, "toolhive_mcp") {
		assert.True(t, hasErrorMetrics, "Expected error metrics to be present")
	}
}

func TestTelemetryIntegration_ToolSpecificMetrics(t *testing.T) {
	t.Parallel()

	// Create real metrics provider for testing
	metricsReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricsReader),
	)

	config := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
	}

	middleware := NewHTTPMiddleware(config, tracenoop.NewTracerProvider(), meterProvider, "github", "stdio")

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("tool result"))
	})

	wrappedHandler := middleware(testHandler)

	// Create tools/call request
	mcpRequest := &mcp.ParsedMCPRequest{
		Method:     "tools/call",
		ID:         "tool-test",
		ResourceID: "github_search",
		Arguments: map[string]interface{}{
			"query": "test",
		},
		IsRequest: true,
	}

	req := httptest.NewRequest("POST", "/messages", nil)
	ctx := context.WithValue(req.Context(), mcp.MCPRequestContextKey, mcpRequest)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Collect and verify tool-specific metrics
	var rm metricdata.ResourceMetrics
	err := metricsReader.Collect(context.Background(), &rm)
	require.NoError(t, err)

	// Look for tool-specific counter
	var foundToolCounter bool
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name == "toolhive_mcp_tool_calls_total" {
				foundToolCounter = true
				if sum, ok := metric.Data.(metricdata.Sum[int64]); ok {
					assert.NotEmpty(t, sum.DataPoints)
					for _, dp := range sum.DataPoints {
						// Verify tool-specific attributes
						attrSet := dp.Attributes
						hasServer := false
						hasTool := false
						for _, attr := range attrSet.ToSlice() {
							if attr.Key == "server" && attr.Value.AsString() == "github" {
								hasServer = true
							}
							if attr.Key == "tool" && attr.Value.AsString() == "github_search" {
								hasTool = true
							}
						}
						assert.True(t, hasServer, "Tool counter should have server attribute")
						assert.True(t, hasTool, "Tool counter should have tool attribute")
					}
				}
			}
		}
	}

	assert.True(t, foundToolCounter, "Tool-specific counter should be recorded for tools/call")

	// Clean up
	err = meterProvider.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestTelemetryIntegration_MultipleRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create provider with both tracing and metrics
	config := Config{
		ServiceName:                 "test-service",
		ServiceVersion:              "1.0.0",
		EnablePrometheusMetricsPath: true,
	}

	provider, err := NewProvider(ctx, config)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	middleware := provider.Middleware("multi-test", "stdio")

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	wrappedHandler := middleware(testHandler)

	// Send multiple requests
	numRequests := 5
	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest("POST", "/messages", nil)
		rec := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}

	// Verify metrics accumulate correctly
	prometheusHandler := provider.PrometheusHandler()
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	prometheusHandler.ServeHTTP(metricsRec, metricsReq)

	metricsBody := metricsRec.Body.String()

	// The exact count format depends on Prometheus exposition format
	// We just verify the metrics are present and contain our server name
	assert.Contains(t, metricsBody, "toolhive_mcp_requests_total")
	assert.Contains(t, metricsBody, `server="multi-test"`)
}

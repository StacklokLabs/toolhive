package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

func init() {
	// Initialize logger for tests
	logger.Initialize()
}

func TestNewAuditor(t *testing.T) {
	t.Parallel()
	config := &Config{}
	auditor, err := NewAuditor(config)

	assert.NoError(t, err)
	assert.NotNil(t, auditor)
	assert.Equal(t, config, auditor.config)
}

func TestAuditorMiddlewareDisabled(t *testing.T) {
	t.Parallel()
	config := &Config{}
	auditor, err := NewAuditor(config)
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	})

	middleware := auditor.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	middleware.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "test response", rr.Body.String())
}

func TestAuditorMiddlewareWithRequestData(t *testing.T) {
	t.Parallel()
	config := &Config{
		IncludeRequestData: true,
		MaxDataSize:        1024,
	}
	auditor, err := NewAuditor(config)
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the body to ensure it's still available
		body := make([]byte, 100)
		n, _ := r.Body.Read(body)
		w.WriteHeader(http.StatusOK)
		w.Write(body[:n])
	})

	middleware := auditor.Middleware(handler)

	requestBody := `{"test": "data"}`
	req := httptest.NewRequest("POST", "/test", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	middleware.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, requestBody, rr.Body.String())
}

func TestAuditorMiddlewareWithResponseData(t *testing.T) {
	t.Parallel()
	config := &Config{
		IncludeResponseData: true,
		MaxDataSize:         1024,
	}
	auditor, err := NewAuditor(config)
	require.NoError(t, err)

	responseData := `{"result": "success"}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseData))
	})

	middleware := auditor.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	middleware.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, responseData, rr.Body.String())
}

func TestDetermineEventType(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	tests := []struct {
		name     string
		path     string
		method   string
		expected string
	}{
		{
			name:     "SSE endpoint",
			path:     "/sse",
			method:   "GET",
			expected: EventTypeMCPInitialize,
		},
		{
			name:     "MCP messages endpoint",
			path:     "/messages",
			method:   "POST",
			expected: "mcp_request", // Since extractMCPMethod returns empty
		},
		{
			name:     "Regular HTTP request",
			path:     "/api/health",
			method:   "GET",
			expected: "http_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			result := auditor.determineEventType(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapMCPMethodToEventType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mcpMethod string
		expected  string
	}{
		{"initialize", EventTypeMCPInitialize},
		{"tools/call", EventTypeMCPToolCall},
		{"tools/list", EventTypeMCPToolsList},
		{"resources/read", EventTypeMCPResourceRead},
		{"resources/list", EventTypeMCPResourcesList},
		{"prompts/get", EventTypeMCPPromptGet},
		{"prompts/list", EventTypeMCPPromptsList},
		{"notifications/message", EventTypeMCPNotification},
		{"ping", EventTypeMCPPing},
		{"logging/setLevel", EventTypeMCPLogging},
		{"completion/complete", EventTypeMCPCompletion},
		{"notifications/roots/list_changed", EventTypeMCPRootsListChanged},
		{"unknown_method", "mcp_request"},
	}

	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)
	for _, tt := range tests {
		t.Run(tt.mcpMethod, func(t *testing.T) {
			t.Parallel()
			result := auditor.mapMCPMethodToEventType(tt.mcpMethod)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetermineOutcome(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	tests := []struct {
		statusCode int
		expected   string
	}{
		{200, OutcomeSuccess},
		{201, OutcomeSuccess},
		{299, OutcomeSuccess},
		{401, OutcomeDenied},
		{403, OutcomeDenied},
		{400, OutcomeFailure},
		{404, OutcomeFailure},
		{499, OutcomeFailure},
		{500, OutcomeError},
		{503, OutcomeError},
		{100, OutcomeSuccess}, // Default case
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.statusCode)), func(t *testing.T) {
			t.Parallel()
			result := auditor.determineOutcome(tt.statusCode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetClientIP(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		expected   string
	}{
		{
			name:     "X-Forwarded-For header",
			headers:  map[string]string{"X-Forwarded-For": "192.168.1.100, 10.0.0.1"},
			expected: "192.168.1.100",
		},
		{
			name:     "X-Real-IP header",
			headers:  map[string]string{"X-Real-IP": "203.0.113.1"},
			expected: "203.0.113.1",
		},
		{
			name:       "RemoteAddr with port",
			remoteAddr: "192.168.1.50:12345",
			expected:   "192.168.1.50",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "192.168.1.60",
			expected:   "192.168.1.60",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/test", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			if tt.remoteAddr != "" {
				req.RemoteAddr = tt.remoteAddr
			}

			result := auditor.getClientIP(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractSubjects(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	t.Run("with JWT claims", func(t *testing.T) {
		t.Parallel()
		claims := jwt.MapClaims{
			"sub":            "user123",
			"name":           "John Doe",
			"email":          "john@example.com",
			"client_name":    "test-client",
			"client_version": "1.0.0",
		}

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), auth.ClaimsContextKey{}, claims)
		req = req.WithContext(ctx)

		subjects := auditor.extractSubjects(req)

		assert.Equal(t, "user123", subjects[SubjectKeyUserID])
		assert.Equal(t, "John Doe", subjects[SubjectKeyUser])
		assert.Equal(t, "test-client", subjects[SubjectKeyClientName])
		assert.Equal(t, "1.0.0", subjects[SubjectKeyClientVersion])
	})

	t.Run("with preferred_username", func(t *testing.T) {
		t.Parallel()
		claims := jwt.MapClaims{
			"sub":                "user456",
			"preferred_username": "johndoe",
		}

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), auth.ClaimsContextKey{}, claims)
		req = req.WithContext(ctx)

		subjects := auditor.extractSubjects(req)

		assert.Equal(t, "user456", subjects[SubjectKeyUserID])
		assert.Equal(t, "johndoe", subjects[SubjectKeyUser])
	})

	t.Run("with email fallback", func(t *testing.T) {
		t.Parallel()
		claims := jwt.MapClaims{
			"sub":   "user789",
			"email": "jane@example.com",
		}

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), auth.ClaimsContextKey{}, claims)
		req = req.WithContext(ctx)

		subjects := auditor.extractSubjects(req)

		assert.Equal(t, "user789", subjects[SubjectKeyUserID])
		assert.Equal(t, "jane@example.com", subjects[SubjectKeyUser])
	})

	t.Run("without claims", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest("GET", "/test", nil)

		subjects := auditor.extractSubjects(req)

		assert.Equal(t, "anonymous", subjects[SubjectKeyUser])
	})
}

func TestDetermineComponent(t *testing.T) {
	t.Parallel()
	t.Run("with configured component", func(t *testing.T) {
		t.Parallel()
		config := &Config{Component: "custom-component"}
		auditor, err := NewAuditor(config)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/test", nil)
		result := auditor.determineComponent(req)

		assert.Equal(t, "custom-component", result)
	})

	t.Run("without configured component", func(t *testing.T) {
		t.Parallel()
		config := &Config{}
		auditor, err := NewAuditor(config)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/test", nil)
		result := auditor.determineComponent(req)

		assert.Equal(t, ComponentToolHive, result)
	})
}

func TestExtractTarget(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	tests := []struct {
		name      string
		path      string
		method    string
		eventType string
		expected  map[string]string
	}{
		{
			name:      "tool call event",
			path:      "/api/tools/calculator",
			method:    "POST",
			eventType: EventTypeMCPToolCall,
			expected: map[string]string{
				TargetKeyEndpoint: "/api/tools/calculator",
				TargetKeyMethod:   "POST",
				TargetKeyType:     TargetTypeTool,
			},
		},
		{
			name:      "resource read event",
			path:      "/api/resources/file.txt",
			method:    "GET",
			eventType: EventTypeMCPResourceRead,
			expected: map[string]string{
				TargetKeyEndpoint: "/api/resources/file.txt",
				TargetKeyMethod:   "GET",
				TargetKeyType:     TargetTypeResource,
			},
		},
		{
			name:      "generic event",
			path:      "/api/health",
			method:    "GET",
			eventType: "http_request",
			expected: map[string]string{
				TargetKeyEndpoint: "/api/health",
				TargetKeyMethod:   "GET",
				TargetKeyType:     "endpoint",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			result := auditor.extractTarget(req, tt.eventType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAddMetadata(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	event := NewAuditEvent("test", EventSource{}, OutcomeSuccess, map[string]string{}, "test")
	req := httptest.NewRequest("GET", "/sse/test", nil)
	duration := 150 * time.Millisecond
	rw := &responseWriter{
		ResponseWriter: httptest.NewRecorder(),
		body:           bytes.NewBufferString("test response"),
	}

	auditor.addMetadata(event, req, duration, rw)

	require.NotNil(t, event.Metadata.Extra)
	assert.Equal(t, int64(150), event.Metadata.Extra[MetadataExtraKeyDuration])
	assert.Equal(t, "sse", event.Metadata.Extra[MetadataExtraKeyTransport])
	assert.Equal(t, 13, event.Metadata.Extra[MetadataExtraKeyResponseSize]) // "test response" length
}

func TestAddEventData(t *testing.T) {
	t.Parallel()
	t.Run("with request and response data", func(t *testing.T) {
		t.Parallel()
		config := &Config{
			IncludeRequestData:  true,
			IncludeResponseData: true,
		}
		auditor, err := NewAuditor(config)
		require.NoError(t, err)

		event := NewAuditEvent("test", EventSource{}, OutcomeSuccess, map[string]string{}, "test")
		req := httptest.NewRequest("POST", "/test", nil)
		requestData := []byte(`{"input": "test"}`)
		rw := &responseWriter{
			body: bytes.NewBufferString(`{"output": "result"}`),
		}

		auditor.addEventData(event, req, rw, requestData)

		require.NotNil(t, event.Data)

		var data map[string]any
		err = json.Unmarshal(*event.Data, &data)
		require.NoError(t, err)

		requestObj, ok := data["request"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "test", requestObj["input"])

		responseObj, ok := data["response"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "result", responseObj["output"])
	})

	t.Run("with non-JSON data", func(t *testing.T) {
		t.Parallel()
		config := &Config{
			IncludeRequestData:  true,
			IncludeResponseData: true,
		}
		auditor, err := NewAuditor(config)
		require.NoError(t, err)

		event := NewAuditEvent("test", EventSource{}, OutcomeSuccess, map[string]string{}, "test")
		req := httptest.NewRequest("POST", "/test", nil)
		requestData := []byte("plain text request")
		rw := &responseWriter{
			body: bytes.NewBufferString("plain text response"),
		}

		auditor.addEventData(event, req, rw, requestData)

		require.NotNil(t, event.Data)

		var data map[string]any
		err = json.Unmarshal(*event.Data, &data)
		require.NoError(t, err)

		assert.Equal(t, "plain text request", data["request"])
		assert.Equal(t, "plain text response", data["response"])
	})

	t.Run("disabled data inclusion", func(t *testing.T) {
		t.Parallel()
		config := &Config{
			IncludeRequestData:  false,
			IncludeResponseData: false,
		}
		auditor, err := NewAuditor(config)
		require.NoError(t, err)

		event := NewAuditEvent("test", EventSource{}, OutcomeSuccess, map[string]string{}, "test")
		req := httptest.NewRequest("POST", "/test", nil)
		requestData := []byte("test data")
		rw := &responseWriter{body: bytes.NewBufferString("response")}

		auditor.addEventData(event, req, rw, requestData)

		assert.Nil(t, event.Data)
	})
}

func TestResponseWriterCapture(t *testing.T) {
	t.Parallel()
	config := &Config{
		IncludeResponseData: true,
		MaxDataSize:         10, // Small limit for testing
	}
	auditor, err := NewAuditor(config)
	require.NoError(t, err)

	rw := &responseWriter{
		ResponseWriter: httptest.NewRecorder(),
		auditor:        auditor,
		body:           &bytes.Buffer{},
	}

	// Write data within limit
	n, err := rw.Write([]byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "test", rw.body.String())

	// Write data that exceeds limit
	n, err = rw.Write([]byte("more data"))
	assert.NoError(t, err)
	assert.Equal(t, 9, n)
	// Should not capture more data due to size limit
	assert.Equal(t, "test", rw.body.String())
}

func TestResponseWriterStatusCode(t *testing.T) {
	t.Parallel()
	rw := &responseWriter{
		ResponseWriter: httptest.NewRecorder(),
		statusCode:     http.StatusOK, // Default
	}

	// Test WriteHeader
	rw.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, rw.statusCode)
}

func TestExtractSourceWithHeaders(t *testing.T) {
	t.Parallel()
	auditor, err := NewAuditor(&Config{})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("User-Agent", "TestAgent/1.0")
	req.Header.Set("X-Request-ID", "req-12345")
	req.RemoteAddr = "192.168.1.100:8080"

	source := auditor.extractSource(req)

	assert.Equal(t, SourceTypeNetwork, source.Type)
	assert.Equal(t, "192.168.1.100", source.Value)
	assert.Equal(t, "TestAgent/1.0", source.Extra[SourceExtraKeyUserAgent])
	assert.Equal(t, "req-12345", source.Extra[SourceExtraKeyRequestID])
}

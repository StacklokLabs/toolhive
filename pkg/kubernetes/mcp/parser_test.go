package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsingMiddleware(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		method         string
		path           string
		contentType    string
		body           string
		expectParsed   bool
		expectedMethod string
		expectedID     interface{}
		expectedResID  string
	}{
		{
			name:           "tools/call request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"weather","arguments":{"location":"NYC"}}}`,
			expectParsed:   true,
			expectedMethod: "tools/call",
			expectedID:     int64(1), // JSON-RPC library uses int64 for numeric IDs
			expectedResID:  "weather",
		},
		{
			name:           "initialize request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test-client","version":"1.0.0"},"capabilities":{}}}`,
			expectParsed:   true,
			expectedMethod: "initialize",
			expectedID:     "init-1",
			expectedResID:  "test-client",
		},
		{
			name:           "resources/read request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"file:///test.txt"}}`,
			expectParsed:   true,
			expectedMethod: "resources/read",
			expectedID:     int64(2),
			expectedResID:  "file:///test.txt",
		},
		{
			name:           "prompts/get request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"greeting","arguments":{"name":"Alice"}}}`,
			expectParsed:   true,
			expectedMethod: "prompts/get",
			expectedID:     int64(3),
			expectedResID:  "greeting",
		},
		{
			name:           "ping request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":4,"method":"ping","params":{}}`,
			expectParsed:   true,
			expectedMethod: "ping",
			expectedID:     int64(4),
			expectedResID:  "ping",
		},
		{
			name:         "GET request - not parsed",
			method:       "GET",
			path:         "/messages",
			contentType:  "application/json",
			body:         "",
			expectParsed: false,
		},
		{
			name:         "non-JSON content type - not parsed",
			method:       "POST",
			path:         "/messages",
			contentType:  "text/plain",
			body:         "not json",
			expectParsed: false,
		},
		{
			name:         "SSE endpoint - not parsed",
			method:       "POST",
			path:         "/sse",
			contentType:  "application/json",
			body:         `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
			expectParsed: false,
		},
		{
			name:         "non-MCP path - not parsed",
			method:       "POST",
			path:         "/health",
			contentType:  "application/json",
			body:         `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
			expectParsed: false,
		},
		{
			name:           "tools/list request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"cursor":"next-page"}}`,
			expectParsed:   true,
			expectedMethod: "tools/list",
			expectedID:     int64(5),
			expectedResID:  "next-page",
		},
		{
			name:           "logging/setLevel request",
			method:         "POST",
			path:           "/messages",
			contentType:    "application/json",
			body:           `{"jsonrpc":"2.0","id":6,"method":"logging/setLevel","params":{"level":"debug"}}`,
			expectParsed:   true,
			expectedMethod: "logging/setLevel",
			expectedID:     int64(6),
			expectedResID:  "debug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create a test handler that captures the context
			var capturedCtx context.Context
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedCtx = r.Context()
				w.WriteHeader(http.StatusOK)
			})

			// Wrap with parsing middleware
			middleware := ParsingMiddleware(testHandler)

			// Create test request
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			w := httptest.NewRecorder()

			// Execute the middleware
			middleware.ServeHTTP(w, req)

			// Check if parsing occurred as expected
			parsed := GetParsedMCPRequest(capturedCtx)
			if tt.expectParsed {
				require.NotNil(t, parsed, "Expected MCP request to be parsed")
				assert.Equal(t, tt.expectedMethod, parsed.Method)
				assert.Equal(t, tt.expectedID, parsed.ID)
				assert.Equal(t, tt.expectedResID, parsed.ResourceID)
				assert.True(t, parsed.IsRequest)
				assert.False(t, parsed.IsBatch)
			} else {
				assert.Nil(t, parsed, "Expected MCP request not to be parsed")
			}
		})
	}
}

func TestExtractResourceAndArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		method             string
		params             string
		expectedResourceID string
		expectedArguments  map[string]interface{}
	}{
		{
			name:               "tools/call with arguments",
			method:             "tools/call",
			params:             `{"name":"weather","arguments":{"location":"NYC","units":"metric"}}`,
			expectedResourceID: "weather",
			expectedArguments: map[string]interface{}{
				"location": "NYC",
				"units":    "metric",
			},
		},
		{
			name:               "initialize with client info",
			method:             "initialize",
			params:             `{"protocolVersion":"2024-11-05","clientInfo":{"name":"test-client","version":"1.0.0"},"capabilities":{}}`,
			expectedResourceID: "test-client",
			expectedArguments: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"clientInfo": map[string]interface{}{
					"name":    "test-client",
					"version": "1.0.0",
				},
				"capabilities": map[string]interface{}{},
			},
		},
		{
			name:               "resources/read with URI",
			method:             "resources/read",
			params:             `{"uri":"file:///test.txt"}`,
			expectedResourceID: "file:///test.txt",
			expectedArguments:  nil,
		},
		{
			name:               "prompts/get with arguments",
			method:             "prompts/get",
			params:             `{"name":"greeting","arguments":{"name":"Alice"}}`,
			expectedResourceID: "greeting",
			expectedArguments: map[string]interface{}{
				"name": "Alice",
			},
		},
		{
			name:               "tools/list with cursor",
			method:             "tools/list",
			params:             `{"cursor":"next-page"}`,
			expectedResourceID: "next-page",
			expectedArguments:  nil,
		},
		{
			name:               "ping with empty params",
			method:             "ping",
			params:             `{}`,
			expectedResourceID: "ping",
			expectedArguments:  nil,
		},
		{
			name:               "progress/update with token",
			method:             "progress/update",
			params:             `{"progressToken":"task-123","progress":50}`,
			expectedResourceID: "task-123",
			expectedArguments:  nil,
		},
		{
			name:               "unknown method",
			method:             "unknown/method",
			params:             `{"someParam":"value"}`,
			expectedResourceID: "",
			expectedArguments:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var params json.RawMessage
			if tt.params != "" {
				params = json.RawMessage(tt.params)
			}

			resourceID, arguments := extractResourceAndArguments(tt.method, params)

			assert.Equal(t, tt.expectedResourceID, resourceID)
			if tt.expectedArguments == nil {
				assert.Nil(t, arguments)
			} else {
				assert.Equal(t, tt.expectedArguments, arguments)
			}
		})
	}
}

func TestConvenienceFunctions(t *testing.T) {
	t.Parallel()
	// Create a context with parsed MCP request
	parsed := &ParsedMCPRequest{
		Method:     "tools/call",
		ID:         "test-id",
		ResourceID: "weather",
		Arguments: map[string]interface{}{
			"location": "NYC",
		},
	}
	ctx := context.WithValue(context.Background(), MCPRequestContextKey, parsed)

	// Test GetMCPMethod
	method := GetMCPMethod(ctx)
	assert.Equal(t, "tools/call", method)

	// Test GetMCPResourceID
	resourceID := GetMCPResourceID(ctx)
	assert.Equal(t, "weather", resourceID)

	// Test GetMCPArguments
	arguments := GetMCPArguments(ctx)
	expected := map[string]interface{}{
		"location": "NYC",
	}
	assert.Equal(t, expected, arguments)

	// Test with empty context
	emptyCtx := context.Background()
	assert.Equal(t, "", GetMCPMethod(emptyCtx))
	assert.Equal(t, "", GetMCPResourceID(emptyCtx))
	assert.Nil(t, GetMCPArguments(emptyCtx))
}

func TestShouldParseMCPRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		expected    bool
	}{
		{
			name:        "POST to /messages with JSON",
			method:      "POST",
			path:        "/messages",
			contentType: "application/json",
			expected:    true,
		},
		{
			name:        "POST to /mcp with JSON",
			method:      "POST",
			path:        "/mcp",
			contentType: "application/json",
			expected:    true,
		},
		{
			name:        "GET request",
			method:      "GET",
			path:        "/messages",
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "POST with non-JSON content type",
			method:      "POST",
			path:        "/messages",
			contentType: "text/plain",
			expected:    false,
		},
		{
			name:        "POST to SSE endpoint",
			method:      "POST",
			path:        "/sse",
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "POST to non-MCP path",
			method:      "POST",
			path:        "/health",
			contentType: "application/json",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Content-Type", tt.contentType)

			result := shouldParseMCPRequest(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseMCPRequestWithInvalidJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty body",
			body: "",
		},
		{
			name: "invalid JSON",
			body: "not json",
		},
		{
			name: "JSON-RPC response instead of request",
			body: `{"jsonrpc":"2.0","id":1,"result":{"success":true}}`,
		},
		{
			name: "JSON-RPC error instead of request",
			body: `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"error"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseMCPRequest([]byte(tt.body))
			assert.Nil(t, result)
		})
	}
}

func TestMiddlewarePreservesRequestBody(t *testing.T) {
	t.Parallel()
	originalBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"weather"}}`

	// Create a test handler that reads the request body
	var capturedBody string
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with parsing middleware
	middleware := ParsingMiddleware(testHandler)

	// Create test request
	req := httptest.NewRequest("POST", "/messages", bytes.NewBufferString(originalBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Execute the middleware
	middleware.ServeHTTP(w, req)

	// Verify the request body was preserved for the next handler
	assert.Equal(t, originalBody, capturedBody)
}

package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	mcpparser "github.com/stacklok/toolhive/pkg/kubernetes/mcp"
)

func TestMiddleware(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()

	// Create a Cedar authorizer
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
			`permit(principal, action == Action::"get_prompt", resource == Prompt::"greeting");`,
			`permit(principal, action == Action::"read_resource", resource == Resource::"data");`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	// Test cases
	testCases := []struct {
		name             string
		method           string
		params           map[string]interface{}
		claims           jwt.MapClaims
		expectStatus     int
		expectAuthorized bool
	}{
		{
			name:   "Authorized tool call",
			method: "tools/call",
			params: map[string]interface{}{
				"name": "weather",
				"arguments": map[string]interface{}{
					"location": "New York",
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Unauthorized tool call",
			method: "tools/call",
			params: map[string]interface{}{
				"name": "calculator",
				"arguments": map[string]interface{}{
					"operation": "add",
					"value1":    5,
					"value2":    10,
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusForbidden,
			expectAuthorized: false,
		},
		{
			name:   "Authorized prompt get",
			method: "prompts/get",
			params: map[string]interface{}{
				"name": "greeting",
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Unauthorized prompt get",
			method: "prompts/get",
			params: map[string]interface{}{
				"name": "farewell",
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusForbidden,
			expectAuthorized: false,
		},
		{
			name:   "Authorized resource read",
			method: "resources/read",
			params: map[string]interface{}{
				"uri": "data",
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Unauthorized resource read",
			method: "resources/read",
			params: map[string]interface{}{
				"uri": "secret",
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusForbidden,
			expectAuthorized: false,
		},
		{
			name:   "Ping is always allowed",
			method: "ping",
			params: map[string]interface{}{},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Initialize is always allowed",
			method: "initialize",
			params: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"roots": map[string]interface{}{
						"listChanged": true,
					},
					"sampling": map[string]interface{}{},
				},
				"clientInfo": map[string]interface{}{
					"name":    "ExampleClient",
					"version": "1.0.0",
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Tools list is always allowed but filtered",
			method: string(mcp.MethodToolsList),
			params: map[string]interface{}{},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Prompts list is always allowed but filtered",
			method: string(mcp.MethodPromptsList),
			params: map[string]interface{}{},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
		{
			name:   "Resources list is always allowed but filtered",
			method: string(mcp.MethodResourcesList),
			params: map[string]interface{}{},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectStatus:     http.StatusOK,
			expectAuthorized: true,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a JSON-RPC request
			paramsJSON, err := json.Marshal(tc.params)
			require.NoError(t, err, "Failed to marshal params")

			request, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), tc.method, json.RawMessage(paramsJSON))
			require.NoError(t, err, "Failed to create JSON-RPC request")

			// Marshal the request to JSON
			requestJSON, err := jsonrpc2.EncodeMessage(request)
			require.NoError(t, err, "Failed to encode JSON-RPC request")

			// Create an HTTP request
			req, err := http.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(requestJSON))
			require.NoError(t, err, "Failed to create HTTP request")
			req.Header.Set("Content-Type", "application/json")

			// Add claims to the request context
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsContextKey{}, tc.claims))

			// Create a response recorder
			rr := httptest.NewRecorder()

			// Create a handler that records if it was called
			var handlerCalled bool
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})

			// Apply the middleware chain: MCP parsing first, then authorization
			middleware := mcpparser.ParsingMiddleware(authorizer.Middleware(handler))

			// Serve the request
			middleware.ServeHTTP(rr, req)

			// Check the response
			assert.Equal(t, tc.expectStatus, rr.Code, "Response status code does not match expected")
			assert.Equal(t, tc.expectAuthorized, handlerCalled, "Handler called status does not match expected")
		})
	}
}

// TestMiddlewareWithGETRequest tests that the middleware doesn't panic with GET requests.
func TestMiddlewareWithGETRequest(t *testing.T) {
	t.Parallel()
	// Create a Cedar authorizer
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	// Create a handler that records if it was called
	var handlerCalled bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Apply the middleware chain: MCP parsing first, then authorization
	middleware := mcpparser.ParsingMiddleware(authorizer.Middleware(handler))

	// Create a GET request
	req, err := http.NewRequest(http.MethodGet, "/messages", nil)
	require.NoError(t, err, "Failed to create HTTP request")

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Serve the request
	middleware.ServeHTTP(rr, req)

	// Check that the handler was called and the response is OK
	assert.True(t, handlerCalled, "Handler should be called for GET requests")
	assert.Equal(t, http.StatusOK, rr.Code, "Response status code should be OK")
}

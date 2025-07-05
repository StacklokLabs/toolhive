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

// TestIntegrationListFiltering demonstrates the complete authorization flow
// with realistic policies and shows how list responses are filtered
func TestIntegrationListFiltering(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()
	// Create a realistic Cedar authorizer with role-based policies
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			// Basic users can only access weather and news tools
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather") when { principal.claim_role == "user" };`,
			`permit(principal, action == Action::"call_tool", resource == Tool::"news") when { principal.claim_role == "user" };`,

			// Admin users can access all tools
			`permit(principal, action == Action::"call_tool", resource) when { principal.claim_role == "admin" };`,

			// Basic users can only access public prompts
			`permit(principal, action == Action::"get_prompt", resource == Prompt::"greeting") when { principal.claim_role == "user" };`,
			`permit(principal, action == Action::"get_prompt", resource == Prompt::"help") when { principal.claim_role == "user" };`,

			// Admin users can access all prompts
			`permit(principal, action == Action::"get_prompt", resource) when { principal.claim_role == "admin" };`,

			// Only admin users can access sensitive resources
			`permit(principal, action == Action::"read_resource", resource == Resource::"public_data") when { principal.claim_role == "user" };`,
			`permit(principal, action == Action::"read_resource", resource) when { principal.claim_role == "admin" };`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	testCases := []struct {
		name          string
		userRole      string
		method        string
		mockResponse  interface{}
		expectedItems []string
		description   string
	}{
		{
			name:     "Basic user sees filtered tools list",
			userRole: "user",
			method:   string(mcp.MethodToolsList),
			mockResponse: mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "weather", Description: "Get weather information"},
					{Name: "news", Description: "Get latest news"},
					{Name: "admin_tool", Description: "Admin-only tool"},
					{Name: "calculator", Description: "Perform calculations"},
					{Name: "database", Description: "Database access"},
				},
			},
			expectedItems: []string{"weather", "news"},
			description:   "Basic user should only see weather and news tools",
		},
		{
			name:     "Admin user sees all tools",
			userRole: "admin",
			method:   string(mcp.MethodToolsList),
			mockResponse: mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "weather", Description: "Get weather information"},
					{Name: "news", Description: "Get latest news"},
					{Name: "admin_tool", Description: "Admin-only tool"},
					{Name: "calculator", Description: "Perform calculations"},
					{Name: "database", Description: "Database access"},
				},
			},
			expectedItems: []string{"weather", "news", "admin_tool", "calculator", "database"},
			description:   "Admin user should see all tools",
		},
		{
			name:     "Basic user sees filtered prompts list",
			userRole: "user",
			method:   string(mcp.MethodPromptsList),
			mockResponse: mcp.ListPromptsResult{
				Prompts: []mcp.Prompt{
					{Name: "greeting", Description: "Generate greetings"},
					{Name: "help", Description: "Generate help text"},
					{Name: "admin_prompt", Description: "Admin-only prompt"},
					{Name: "system_prompt", Description: "System configuration prompt"},
				},
			},
			expectedItems: []string{"greeting", "help"},
			description:   "Basic user should only see public prompts",
		},
		{
			name:     "Admin user sees all prompts",
			userRole: "admin",
			method:   string(mcp.MethodPromptsList),
			mockResponse: mcp.ListPromptsResult{
				Prompts: []mcp.Prompt{
					{Name: "greeting", Description: "Generate greetings"},
					{Name: "help", Description: "Generate help text"},
					{Name: "admin_prompt", Description: "Admin-only prompt"},
					{Name: "system_prompt", Description: "System configuration prompt"},
				},
			},
			expectedItems: []string{"greeting", "help", "admin_prompt", "system_prompt"},
			description:   "Admin user should see all prompts",
		},
		{
			name:     "Basic user sees filtered resources list",
			userRole: "user",
			method:   string(mcp.MethodResourcesList),
			mockResponse: mcp.ListResourcesResult{
				Resources: []mcp.Resource{
					{URI: "public_data", Name: "Public Data"},
					{URI: "private_data", Name: "Private Data"},
					{URI: "admin_config", Name: "Admin Configuration"},
					{URI: "user_logs", Name: "User Logs"},
				},
			},
			expectedItems: []string{"public_data"},
			description:   "Basic user should only see public resources",
		},
		{
			name:     "Admin user sees all resources",
			userRole: "admin",
			method:   string(mcp.MethodResourcesList),
			mockResponse: mcp.ListResourcesResult{
				Resources: []mcp.Resource{
					{URI: "public_data", Name: "Public Data"},
					{URI: "private_data", Name: "Private Data"},
					{URI: "admin_config", Name: "Admin Configuration"},
					{URI: "user_logs", Name: "User Logs"},
				},
			},
			expectedItems: []string{"public_data", "private_data", "admin_config", "user_logs"},
			description:   "Admin user should see all resources",
		},
		{
			name:     "Unknown user with no permissions sees empty tools list",
			userRole: "guest",
			method:   string(mcp.MethodToolsList),
			mockResponse: mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "weather", Description: "Get weather information"},
					{Name: "news", Description: "Get latest news"},
					{Name: "admin_tool", Description: "Admin-only tool"},
					{Name: "calculator", Description: "Perform calculations"},
					{Name: "database", Description: "Database access"},
				},
			},
			expectedItems: []string{}, // Empty list - no permissions
			description:   "Guest user with no defined permissions should see no tools",
		},
		{
			name:     "Unknown user with no permissions sees empty prompts list",
			userRole: "guest",
			method:   string(mcp.MethodPromptsList),
			mockResponse: mcp.ListPromptsResult{
				Prompts: []mcp.Prompt{
					{Name: "greeting", Description: "Generate greetings"},
					{Name: "help", Description: "Generate help text"},
					{Name: "admin_prompt", Description: "Admin-only prompt"},
					{Name: "system_prompt", Description: "System configuration prompt"},
				},
			},
			expectedItems: []string{}, // Empty list - no permissions
			description:   "Guest user with no defined permissions should see no prompts",
		},
		{
			name:     "Unknown user with no permissions sees empty resources list",
			userRole: "guest",
			method:   string(mcp.MethodResourcesList),
			mockResponse: mcp.ListResourcesResult{
				Resources: []mcp.Resource{
					{URI: "public_data", Name: "Public Data"},
					{URI: "private_data", Name: "Private Data"},
					{URI: "admin_config", Name: "Admin Configuration"},
					{URI: "user_logs", Name: "User Logs"},
				},
			},
			expectedItems: []string{}, // Empty list - no permissions
			description:   "Guest user with no defined permissions should see no resources",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create JWT claims for the test user
			claims := jwt.MapClaims{
				"sub":  "testuser123",
				"name": "Test User",
				"role": tc.userRole,
			}

			// Create a mock MCP server response
			responseData, err := json.Marshal(tc.mockResponse)
			require.NoError(t, err, "Failed to marshal mock response")

			jsonrpcResponse := &jsonrpc2.Response{
				ID:     jsonrpc2.Int64ID(1),
				Result: json.RawMessage(responseData),
			}

			responseBytes, err := json.Marshal(jsonrpcResponse)
			require.NoError(t, err, "Failed to marshal JSON-RPC response")

			// Create a mock MCP server that returns the test data
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write(responseBytes)
				require.NoError(t, err, "Failed to write mock response")
			}))
			defer mockServer.Close()

			// Create a JSON-RPC request for the list operation
			listRequest, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), tc.method, json.RawMessage(`{}`))
			require.NoError(t, err, "Failed to create JSON-RPC request")

			requestJSON, err := jsonrpc2.EncodeMessage(listRequest)
			require.NoError(t, err, "Failed to encode JSON-RPC request")

			// Create an HTTP request with JWT claims in context
			req, err := http.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(requestJSON))
			require.NoError(t, err, "Failed to create HTTP request")
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsContextKey{}, claims))

			// Create a response recorder
			rr := httptest.NewRecorder()

			// Create a mock handler that simulates the MCP server response
			mockHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write(responseBytes)
				require.NoError(t, err, "Failed to write mock handler response")
			})

			// Apply the middleware chain: MCP parsing first, then authorization
			middleware := mcpparser.ParsingMiddleware(authorizer.Middleware(mockHandler))

			// Execute the request through the middleware
			middleware.ServeHTTP(rr, req)

			// Verify the response was successful
			assert.Equal(t, http.StatusOK, rr.Code, "Response should be successful")

			// Parse the filtered response
			var filteredResponse jsonrpc2.Response
			err = json.Unmarshal(rr.Body.Bytes(), &filteredResponse)
			require.NoError(t, err, "Failed to unmarshal filtered response")

			// Verify no error in the response
			assert.Nil(t, filteredResponse.Error, "Response should not have an error")
			assert.NotNil(t, filteredResponse.Result, "Response should have a result")

			// Parse and verify the filtered items based on the method type
			switch tc.method {
			case string(mcp.MethodToolsList):
				var result mcp.ListToolsResult
				err = json.Unmarshal(filteredResponse.Result, &result)
				require.NoError(t, err, "Failed to unmarshal tools result")

				actualNames := make([]string, len(result.Tools))
				for i, tool := range result.Tools {
					actualNames[i] = tool.Name
				}

				assert.ElementsMatch(t, tc.expectedItems, actualNames,
					"Filtered tools should match expected items. %s", tc.description)

			case string(mcp.MethodPromptsList):
				var result mcp.ListPromptsResult
				err = json.Unmarshal(filteredResponse.Result, &result)
				require.NoError(t, err, "Failed to unmarshal prompts result")

				actualNames := make([]string, len(result.Prompts))
				for i, prompt := range result.Prompts {
					actualNames[i] = prompt.Name
				}

				assert.ElementsMatch(t, tc.expectedItems, actualNames,
					"Filtered prompts should match expected items. %s", tc.description)

			case string(mcp.MethodResourcesList):
				var result mcp.ListResourcesResult
				err = json.Unmarshal(filteredResponse.Result, &result)
				require.NoError(t, err, "Failed to unmarshal resources result")

				actualURIs := make([]string, len(result.Resources))
				for i, resource := range result.Resources {
					actualURIs[i] = resource.URI
				}

				assert.ElementsMatch(t, tc.expectedItems, actualURIs,
					"Filtered resources should match expected items. %s", tc.description)
			}

			t.Logf("✅ %s: Expected %d items, got %d items",
				tc.description, len(tc.expectedItems), len(tc.expectedItems))
		})
	}
}

// TestIntegrationNonListOperations verifies that non-list operations still work correctly
func TestIntegrationNonListOperations(t *testing.T) {
	t.Parallel()
	// Create a Cedar authorizer with specific permissions
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather") when { principal.claim_role == "user" };`,
			`permit(principal, action == Action::"call_tool", resource) when { principal.claim_role == "admin" };`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	testCases := []struct {
		name          string
		userRole      string
		method        string
		toolName      string
		expectAllowed bool
		description   string
	}{
		{
			name:          "Basic user can call allowed tool",
			userRole:      "user",
			method:        string(mcp.MethodToolsCall),
			toolName:      "weather",
			expectAllowed: true,
			description:   "Basic user should be able to call weather tool",
		},
		{
			name:          "Basic user cannot call restricted tool",
			userRole:      "user",
			method:        string(mcp.MethodToolsCall),
			toolName:      "admin_tool",
			expectAllowed: false,
			description:   "Basic user should not be able to call admin tool",
		},
		{
			name:          "Admin user can call any tool",
			userRole:      "admin",
			method:        string(mcp.MethodToolsCall),
			toolName:      "admin_tool",
			expectAllowed: true,
			description:   "Admin user should be able to call any tool",
		},
		{
			name:          "Guest user with no permissions cannot call any tool",
			userRole:      "guest",
			method:        string(mcp.MethodToolsCall),
			toolName:      "weather",
			expectAllowed: false,
			description:   "Guest user with no defined permissions should not be able to call any tool",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create JWT claims for the test user
			claims := jwt.MapClaims{
				"sub":  "testuser123",
				"name": "Test User",
				"role": tc.userRole,
			}

			// Create a JSON-RPC request for the tool call
			params := map[string]interface{}{
				"name": tc.toolName,
				"arguments": map[string]interface{}{
					"location": "New York",
				},
			}
			paramsJSON, err := json.Marshal(params)
			require.NoError(t, err, "Failed to marshal params")

			callRequest, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), tc.method, json.RawMessage(paramsJSON))
			require.NoError(t, err, "Failed to create JSON-RPC request")

			requestJSON, err := jsonrpc2.EncodeMessage(callRequest)
			require.NoError(t, err, "Failed to encode JSON-RPC request")

			// Create an HTTP request with JWT claims in context
			req, err := http.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(requestJSON))
			require.NoError(t, err, "Failed to create HTTP request")
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsContextKey{}, claims))

			// Create a response recorder
			rr := httptest.NewRecorder()

			// Create a mock handler that would be called if authorized
			var handlerCalled bool
			mockHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(`{"result": "success"}`))
				require.NoError(t, err, "Failed to write mock response")
			})

			// Apply the middleware chain: MCP parsing first, then authorization
			middleware := mcpparser.ParsingMiddleware(authorizer.Middleware(mockHandler))

			// Execute the request through the middleware
			middleware.ServeHTTP(rr, req)

			// Verify the authorization result
			if tc.expectAllowed {
				assert.Equal(t, http.StatusOK, rr.Code, "Request should be authorized")
				assert.True(t, handlerCalled, "Handler should be called for authorized request")
				t.Logf("✅ %s: Request was correctly authorized", tc.description)
			} else {
				assert.Equal(t, http.StatusForbidden, rr.Code, "Request should be forbidden")
				assert.False(t, handlerCalled, "Handler should not be called for unauthorized request")
				t.Logf("✅ %s: Request was correctly denied", tc.description)
			}
		})
	}
}

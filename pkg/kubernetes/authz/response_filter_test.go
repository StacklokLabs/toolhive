package authz

import (
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
)

func TestResponseFilteringWriter(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()

	// Create a Cedar authorizer with specific tool permissions
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
			`permit(principal, action == Action::"get_prompt", resource == Prompt::"greeting");`,
			`permit(principal, action == Action::"read_resource", resource == Resource::"data");`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	testCases := []struct {
		name           string
		method         string
		responseData   interface{}
		claims         jwt.MapClaims
		expectedResult interface{}
	}{
		{
			name:   "Filter tools list - user can access weather tool only",
			method: string(mcp.MethodToolsList),
			responseData: mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "weather", Description: "Get weather information"},
					{Name: "calculator", Description: "Perform calculations"},
					{Name: "translator", Description: "Translate text"},
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectedResult: mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "weather", Description: "Get weather information"},
				},
			},
		},
		{
			name:   "Filter prompts list - user can access greeting prompt only",
			method: string(mcp.MethodPromptsList),
			responseData: mcp.ListPromptsResult{
				Prompts: []mcp.Prompt{
					{Name: "greeting", Description: "Generate greetings"},
					{Name: "farewell", Description: "Generate farewells"},
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectedResult: mcp.ListPromptsResult{
				Prompts: []mcp.Prompt{
					{Name: "greeting", Description: "Generate greetings"},
				},
			},
		},
		{
			name:   "Filter resources list - user can access data resource only",
			method: string(mcp.MethodResourcesList),
			responseData: mcp.ListResourcesResult{
				Resources: []mcp.Resource{
					{URI: "data", Name: "Data Resource"},
					{URI: "secret", Name: "Secret Resource"},
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectedResult: mcp.ListResourcesResult{
				Resources: []mcp.Resource{
					{URI: "data", Name: "Data Resource"},
				},
			},
		},
		{
			name:   "Empty tools list when user has no permissions",
			method: string(mcp.MethodToolsList),
			responseData: mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "calculator", Description: "Perform calculations"},
					{Name: "translator", Description: "Translate text"},
				},
			},
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
			},
			expectedResult: mcp.ListToolsResult{
				Tools: []mcp.Tool{}, // Empty list since user can't access any of these tools
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a JSON-RPC response with the test data
			responseData, err := json.Marshal(tc.responseData)
			require.NoError(t, err, "Failed to marshal response data")

			jsonrpcResponse := &jsonrpc2.Response{
				ID:     jsonrpc2.Int64ID(1),
				Result: json.RawMessage(responseData),
			}

			responseBytes, err := json.Marshal(jsonrpcResponse)
			require.NoError(t, err, "Failed to marshal JSON-RPC response")

			// Create an HTTP request with claims in context
			req, err := http.NewRequest(http.MethodPost, "/messages", nil)
			require.NoError(t, err, "Failed to create HTTP request")
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsContextKey{}, tc.claims))

			// Create a response recorder
			rr := httptest.NewRecorder()

			// Create the response filtering writer
			filteringWriter := NewResponseFilteringWriter(rr, authorizer, req, tc.method)

			// Write the response data
			_, err = filteringWriter.Write(responseBytes)
			require.NoError(t, err, "Failed to write response data")

			// Flush the response
			err = filteringWriter.Flush()
			require.NoError(t, err, "Failed to flush response")

			// Parse the filtered response
			var filteredResponse jsonrpc2.Response
			err = json.Unmarshal(rr.Body.Bytes(), &filteredResponse)
			require.NoError(t, err, "Failed to unmarshal filtered response")

			// Verify the response was filtered correctly
			assert.Nil(t, filteredResponse.Error, "Response should not have an error")
			assert.NotNil(t, filteredResponse.Result, "Response should have a result")

			// Parse the result based on the method type
			switch tc.method {
			case string(mcp.MethodToolsList):
				var actualResult mcp.ListToolsResult
				err = json.Unmarshal(filteredResponse.Result, &actualResult)
				require.NoError(t, err, "Failed to unmarshal tools result")

				expectedResult := tc.expectedResult.(mcp.ListToolsResult)
				assert.Equal(t, len(expectedResult.Tools), len(actualResult.Tools), "Tool count should match")
				for i, expectedTool := range expectedResult.Tools {
					if i < len(actualResult.Tools) {
						assert.Equal(t, expectedTool.Name, actualResult.Tools[i].Name, "Tool name should match")
						assert.Equal(t, expectedTool.Description, actualResult.Tools[i].Description, "Tool description should match")
					}
				}

			case string(mcp.MethodPromptsList):
				var actualResult mcp.ListPromptsResult
				err = json.Unmarshal(filteredResponse.Result, &actualResult)
				require.NoError(t, err, "Failed to unmarshal prompts result")

				expectedResult := tc.expectedResult.(mcp.ListPromptsResult)
				assert.Equal(t, len(expectedResult.Prompts), len(actualResult.Prompts), "Prompt count should match")
				for i, expectedPrompt := range expectedResult.Prompts {
					if i < len(actualResult.Prompts) {
						assert.Equal(t, expectedPrompt.Name, actualResult.Prompts[i].Name, "Prompt name should match")
						assert.Equal(t, expectedPrompt.Description, actualResult.Prompts[i].Description, "Prompt description should match")
					}
				}

			case string(mcp.MethodResourcesList):
				var actualResult mcp.ListResourcesResult
				err = json.Unmarshal(filteredResponse.Result, &actualResult)
				require.NoError(t, err, "Failed to unmarshal resources result")

				expectedResult := tc.expectedResult.(mcp.ListResourcesResult)
				assert.Equal(t, len(expectedResult.Resources), len(actualResult.Resources), "Resource count should match")
				for i, expectedResource := range expectedResult.Resources {
					if i < len(actualResult.Resources) {
						assert.Equal(t, expectedResource.URI, actualResult.Resources[i].URI, "Resource URI should match")
						assert.Equal(t, expectedResource.Name, actualResult.Resources[i].Name, "Resource name should match")
					}
				}
			}
		})
	}
}

func TestResponseFilteringWriter_NonListOperations(t *testing.T) {
	t.Parallel()
	// Create a Cedar authorizer
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	// Test that non-list operations pass through unchanged
	testData := map[string]interface{}{
		"result": "some result data",
	}

	responseData, err := json.Marshal(testData)
	require.NoError(t, err, "Failed to marshal response data")

	jsonrpcResponse := &jsonrpc2.Response{
		ID:     jsonrpc2.Int64ID(1),
		Result: json.RawMessage(responseData),
	}

	responseBytes, err := json.Marshal(jsonrpcResponse)
	require.NoError(t, err, "Failed to marshal JSON-RPC response")

	// Create an HTTP request
	req, err := http.NewRequest(http.MethodPost, "/messages", nil)
	require.NoError(t, err, "Failed to create HTTP request")

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Create the response filtering writer for a non-list operation
	filteringWriter := NewResponseFilteringWriter(rr, authorizer, req, "tools/call")

	// Write the response data
	_, err = filteringWriter.Write(responseBytes)
	require.NoError(t, err, "Failed to write response data")

	// Flush the response
	err = filteringWriter.Flush()
	require.NoError(t, err, "Failed to flush response")

	// Verify the response passed through unchanged
	assert.Equal(t, responseBytes, rr.Body.Bytes(), "Non-list response should pass through unchanged")
}

func TestResponseFilteringWriter_ErrorResponse(t *testing.T) {
	t.Parallel()
	// Create a Cedar authorizer
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies: []string{
			`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
		},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	// Create an error response
	jsonrpcResponse := &jsonrpc2.Response{
		ID:    jsonrpc2.Int64ID(1),
		Error: jsonrpc2.NewError(404, "Not found"),
	}

	responseBytes, err := json.Marshal(jsonrpcResponse)
	require.NoError(t, err, "Failed to marshal JSON-RPC response")

	// Create an HTTP request
	req, err := http.NewRequest(http.MethodPost, "/messages", nil)
	require.NoError(t, err, "Failed to create HTTP request")

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Create the response filtering writer
	filteringWriter := NewResponseFilteringWriter(rr, authorizer, req, "tools/list")

	// Write the response data
	_, err = filteringWriter.Write(responseBytes)
	require.NoError(t, err, "Failed to write response data")

	// Flush the response
	err = filteringWriter.Flush()
	require.NoError(t, err, "Failed to flush response")

	// Verify the error response passed through unchanged
	assert.Equal(t, responseBytes, rr.Body.Bytes(), "Error response should pass through unchanged")
}

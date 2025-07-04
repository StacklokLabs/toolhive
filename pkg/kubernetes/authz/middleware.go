// Package authz provides authorization utilities using Cedar policies.
package authz

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/kubernetes/mcp"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport/ssecommon"
)

// MCPMethodToFeatureOperation maps MCP method names to feature and operation pairs.
var MCPMethodToFeatureOperation = map[string]struct {
	Feature   MCPFeature
	Operation MCPOperation
}{
	"tools/call":      {Feature: MCPFeatureTool, Operation: MCPOperationCall},
	"tools/list":      {Feature: MCPFeatureTool, Operation: MCPOperationList},
	"prompts/get":     {Feature: MCPFeaturePrompt, Operation: MCPOperationGet},
	"prompts/list":    {Feature: MCPFeaturePrompt, Operation: MCPOperationList},
	"resources/read":  {Feature: MCPFeatureResource, Operation: MCPOperationRead},
	"resources/list":  {Feature: MCPFeatureResource, Operation: MCPOperationList},
	"features/list":   {Feature: "", Operation: MCPOperationList},
	"ping":            {Feature: "", Operation: ""}, // Always allowed
	"progress/update": {Feature: "", Operation: ""}, // Always allowed
	"initialize":      {Feature: "", Operation: ""}, // Always allowed
}

// shouldSkipInitialAuthorization checks if the request should skip authorization
// before reading the request body.
func shouldSkipInitialAuthorization(r *http.Request) bool {
	// Skip authorization for non-POST requests and non-JSON content types
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return true
	}

	// Skip authorization for the SSE endpoint
	if strings.HasSuffix(r.URL.Path, ssecommon.HTTPSSEEndpoint) {
		return true
	}

	return false
}

// shouldSkipSubsequentAuthorization checks if the request should skip authorization
// after parsing the JSON-RPC message.
func shouldSkipSubsequentAuthorization(method string) bool {
	// Skip authorization for methods that don't require it
	if method == "ping" || method == "progress/update" || method == "initialize" {
		return true
	}

	return false
}

// convertToJSONRPC2ID converts an interface{} ID to jsonrpc2.ID
func convertToJSONRPC2ID(id interface{}) (jsonrpc2.ID, error) {
	if id == nil {
		return jsonrpc2.ID{}, nil
	}

	switch v := id.(type) {
	case string:
		return jsonrpc2.StringID(v), nil
	case int:
		return jsonrpc2.Int64ID(int64(v)), nil
	case int64:
		return jsonrpc2.Int64ID(v), nil
	case float64:
		// JSON numbers are often unmarshaled as float64
		return jsonrpc2.Int64ID(int64(v)), nil
	default:
		return jsonrpc2.ID{}, fmt.Errorf("unsupported ID type: %T", id)
	}
}

// handleUnauthorized handles unauthorized requests.
func handleUnauthorized(w http.ResponseWriter, msgID interface{}, err error) {
	// Create an error response
	errorMsg := "Unauthorized"
	if err != nil {
		errorMsg = err.Error()
	}

	// Create a JSON-RPC error response
	id, err := convertToJSONRPC2ID(msgID)
	if err != nil {
		id = jsonrpc2.ID{} // Use empty ID if conversion fails
	}

	errorResponse := &jsonrpc2.Response{
		ID:    id,
		Error: jsonrpc2.NewError(403, errorMsg),
	}

	// Set the response headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)

	// Write the error response
	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		// If we can't encode the error response, log it and return a simple error
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// Middleware creates an HTTP middleware that authorizes MCP requests using Cedar policies.
// This middleware extracts the MCP message from the request, determines the feature,
// operation, and resource ID, and authorizes the request using Cedar policies.
//
// For list operations (tools/list, prompts/list, resources/list), the middleware allows
// the request to proceed but intercepts the response to filter out items that the user
// is not authorized to access based on the corresponding call/get/read policies.
//
// Example usage:
//
//	// Create a Cedar authorizer with a policy that covers all tools and resources
//	cedarAuthorizer, _ := authz.NewCedarAuthorizer(authz.CedarAuthorizerConfig{
//	    Policies: []string{
//	        `permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
//	        `permit(principal, action == Action::"get_prompt", resource == Prompt::"greeting");`,
//	        `permit(principal, action == Action::"read_resource", resource == Resource::"data");`,
//	    },
//	})
//
//	// Create a transport with the middleware
//	middlewares := []types.Middleware{
//	    jwtValidator.Middleware, // JWT middleware should be applied first
//	    cedarAuthorizer.Middleware, // Cedar middleware is applied second
//	}
//
//	proxy := httpsse.NewHTTPSSEProxy(8080, "my-container", middlewares...)
//	proxy.Start(context.Background())
func (a *CedarAuthorizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if we should skip authorization before checking parsed data
		if shouldSkipInitialAuthorization(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Get parsed MCP request from context (set by parsing middleware)
		parsedRequest := mcp.GetParsedMCPRequest(r.Context())
		if parsedRequest == nil {
			// No parsed MCP request available for a request that should have been parsed
			// This indicates either a malformed request or missing parsing middleware
			http.Error(w, "Invalid or malformed MCP request", http.StatusBadRequest)
			return
		}

		// Check if we should skip authorization after parsing the message
		if shouldSkipSubsequentAuthorization(parsedRequest.Method) {
			next.ServeHTTP(w, r)
			return
		}

		// Get the feature and operation from the method
		featureOp, ok := MCPMethodToFeatureOperation[parsedRequest.Method]
		if !ok {
			// Unknown method, let the next handler deal with it
			next.ServeHTTP(w, r)
			return
		}

		// Handle list operations differently - allow them through but filter the response
		if featureOp.Operation == MCPOperationList {
			// Create a response filtering writer to intercept and filter the response
			filteringWriter := NewResponseFilteringWriter(w, a, r, parsedRequest.Method)

			// Call the next handler with the filtering writer
			next.ServeHTTP(filteringWriter, r)

			// Flush the filtered response
			if err := filteringWriter.Flush(); err != nil {
				// If flushing fails, we've already started writing the response,
				// so we can't return an error response. Just log it.
				// In a real application, you might want to use a proper logger here.
				fmt.Printf("Error flushing filtered response: %v\n", err)
			}
			return
		}

		// For non-list operations, perform authorization using parsed data
		// Authorize the request
		authorized, err := a.AuthorizeWithJWTClaims(
			r.Context(),
			featureOp.Feature,
			featureOp.Operation,
			parsedRequest.ResourceID,
			parsedRequest.Arguments,
		)

		// Handle unauthorized requests
		if err != nil || !authorized {
			handleUnauthorized(w, parsedRequest.ID, err)
			return
		}

		// Call the next handler
		next.ServeHTTP(w, r)
	})
}

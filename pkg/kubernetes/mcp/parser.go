// Package mcp provides MCP (Model Context Protocol) parsing utilities and middleware.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/kubernetes/transport/ssecommon"
)

// contextKey is a type for context keys to avoid collisions.
type contextKey string

const (
	// MCPRequestContextKey is the context key for storing parsed MCP request data.
	MCPRequestContextKey contextKey = "mcp_request"
)

// ParsedMCPRequest contains the parsed MCP request information.
type ParsedMCPRequest struct {
	// Method is the MCP method name (e.g., "tools/call", "resources/read")
	Method string
	// ID is the JSON-RPC request ID
	ID interface{}
	// Params contains the raw JSON parameters
	Params json.RawMessage
	// ResourceID is the extracted resource identifier (tool name, resource URI, etc.)
	ResourceID string
	// Arguments contains the extracted arguments for the operation
	Arguments map[string]interface{}
	// IsRequest indicates if this is a JSON-RPC request (vs response or notification)
	IsRequest bool
	// IsBatch indicates if this is a batch request
	IsBatch bool
}

// ParsingMiddleware creates an HTTP middleware that parses MCP JSON-RPC requests
// and stores the parsed information in the request context for use by downstream
// middleware (authorization, audit, etc.).
//
// The middleware:
// 1. Checks if the request should be parsed (POST with JSON content to MCP endpoints)
// 2. Reads and parses the JSON-RPC message
// 3. Extracts method, parameters, and resource information
// 4. Stores the parsed data in request context
// 5. Restores the request body for downstream handlers
//
// Example usage:
//
//	middlewares := []types.Middleware{
//	    authMiddleware,        // Authentication first
//	    mcp.ParsingMiddleware, // MCP parsing after auth
//	    authzMiddleware,       // Authorization uses parsed data
//	    auditMiddleware,       // Audit uses parsed data
//	}
func ParsingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if we should parse this request
		if !shouldParseMCPRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Read the request body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			// If we can't read the body, let the next handler deal with it
			next.ServeHTTP(w, r)
			return
		}

		// Restore the request body for downstream handlers
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Parse the MCP request and store in context
		parsedRequest := parseMCPRequest(bodyBytes)
		if parsedRequest != nil {
			ctx := context.WithValue(r.Context(), MCPRequestContextKey, parsedRequest)
			r = r.WithContext(ctx)
		}

		// Call the next handler
		next.ServeHTTP(w, r)
	})
}

// GetParsedMCPRequest retrieves the parsed MCP request from the request context.
// Returns nil if no parsed request is available.
func GetParsedMCPRequest(ctx context.Context) *ParsedMCPRequest {
	if parsed, ok := ctx.Value(MCPRequestContextKey).(*ParsedMCPRequest); ok {
		return parsed
	}
	return nil
}

// shouldParseMCPRequest determines if the request should be parsed as an MCP request.
func shouldParseMCPRequest(r *http.Request) bool {
	// Only parse POST requests with JSON content type
	if r.Method != http.MethodPost {
		return false
	}

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		return false
	}

	// Skip SSE endpoint establishment requests
	if strings.HasSuffix(r.URL.Path, ssecommon.HTTPSSEEndpoint) {
		return false
	}

	// Parse requests to MCP message endpoints
	return strings.Contains(r.URL.Path, "/messages") ||
		strings.Contains(r.URL.Path, "/message") ||
		strings.Contains(r.URL.Path, "/mcp")
}

// parseMCPRequest parses the JSON-RPC message and extracts MCP-specific information.
func parseMCPRequest(bodyBytes []byte) *ParsedMCPRequest {
	if len(bodyBytes) == 0 {
		return nil
	}

	// Try to parse as JSON-RPC message
	msg, err := jsonrpc2.DecodeMessage(bodyBytes)
	if err != nil {
		return nil
	}

	// Handle only request messages
	req, ok := msg.(*jsonrpc2.Request)
	if !ok {
		return nil
	}

	// Extract resource ID and arguments based on the method
	resourceID, arguments := extractResourceAndArguments(req.Method, req.Params)

	return &ParsedMCPRequest{
		Method:     req.Method,
		ID:         req.ID.Raw(),
		Params:     req.Params,
		ResourceID: resourceID,
		Arguments:  arguments,
		IsRequest:  true,
		IsBatch:    false, // TODO: Add batch request support if needed
	}
}

// extractResourceAndArguments extracts the resource ID and arguments from the JSON-RPC params
// based on the MCP method type.
// methodHandler defines a function type for handling specific MCP methods
type methodHandler func(map[string]interface{}) (string, map[string]interface{})

// methodHandlers maps MCP methods to their respective handlers
var methodHandlers = map[string]methodHandler{
	"initialize":            handleInitializeMethod,
	"tools/call":            handleNamedResourceMethod,
	"prompts/get":           handleNamedResourceMethod,
	"resources/read":        handleResourceReadMethod,
	"resources/list":        handleListMethod,
	"tools/list":            handleListMethod,
	"prompts/list":          handleListMethod,
	"progress/update":       handleProgressMethod,
	"notifications/message": handleNotificationMethod,
	"logging/setLevel":      handleLoggingMethod,
	"completion/complete":   handleCompletionMethod,
}

// staticResourceIDs maps methods to their static resource IDs
var staticResourceIDs = map[string]string{
	"ping":                             "ping",
	"notifications/roots/list_changed": "roots",
	"notifications/initialized":        "initialized",
}

func extractResourceAndArguments(method string, params json.RawMessage) (string, map[string]interface{}) {
	if params == nil {
		return getStaticResourceID(method), nil
	}

	var paramsMap map[string]interface{}
	if err := json.Unmarshal(params, &paramsMap); err != nil {
		return getStaticResourceID(method), nil
	}

	return processMethodWithHandler(method, paramsMap)
}

// getStaticResourceID returns the static resource ID for methods that don't need parameter parsing
func getStaticResourceID(method string) string {
	if resourceID, exists := staticResourceIDs[method]; exists {
		return resourceID
	}
	return ""
}

// processMethodWithHandler processes the method using the appropriate handler
func processMethodWithHandler(method string, paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if handler, exists := methodHandlers[method]; exists {
		return handler(paramsMap)
	}
	return getStaticResourceID(method), nil
}

// handleInitializeMethod extracts resource ID and arguments for initialize method
func handleInitializeMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	var resourceID string
	if clientInfo, ok := paramsMap["clientInfo"].(map[string]interface{}); ok {
		if name, ok := clientInfo["name"].(string); ok {
			resourceID = name
		}
	}
	return resourceID, paramsMap
}

// handleNamedResourceMethod extracts resource ID and arguments for methods with name parameter
func handleNamedResourceMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	var resourceID string
	var arguments map[string]interface{}

	if name, ok := paramsMap["name"].(string); ok {
		resourceID = name
	}
	if args, ok := paramsMap["arguments"].(map[string]interface{}); ok {
		arguments = args
	}

	return resourceID, arguments
}

// handleResourceReadMethod extracts resource ID for resource read operations
func handleResourceReadMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if uri, ok := paramsMap["uri"].(string); ok {
		return uri, nil
	}
	return "", nil
}

// handleListMethod extracts resource ID for list operations
func handleListMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if cursor, ok := paramsMap["cursor"].(string); ok && cursor != "" {
		return cursor, nil
	}
	return "", nil
}

// handleProgressMethod extracts resource ID for progress updates
func handleProgressMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if token, ok := paramsMap["progressToken"].(string); ok {
		return token, nil
	}
	return "", nil
}

// handleNotificationMethod extracts resource ID for notification messages
func handleNotificationMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if notifMethod, ok := paramsMap["method"].(string); ok {
		return notifMethod, nil
	}
	return "", nil
}

// handleLoggingMethod extracts resource ID for logging operations
func handleLoggingMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if level, ok := paramsMap["level"].(string); ok {
		return level, nil
	}
	return "", nil
}

// handleCompletionMethod extracts resource ID for completion requests
func handleCompletionMethod(paramsMap map[string]interface{}) (string, map[string]interface{}) {
	if ref, ok := paramsMap["ref"].(string); ok {
		return ref, nil
	}
	return "", nil
}

// GetMCPMethod is a convenience function to get the MCP method from the context.
func GetMCPMethod(ctx context.Context) string {
	if parsed := GetParsedMCPRequest(ctx); parsed != nil {
		return parsed.Method
	}
	return ""
}

// GetMCPResourceID is a convenience function to get the MCP resource ID from the context.
func GetMCPResourceID(ctx context.Context) string {
	if parsed := GetParsedMCPRequest(ctx); parsed != nil {
		return parsed.ResourceID
	}
	return ""
}

// GetMCPArguments is a convenience function to get the MCP arguments from the context.
func GetMCPArguments(ctx context.Context) map[string]interface{} {
	if parsed := GetParsedMCPRequest(ctx); parsed != nil {
		return parsed.Arguments
	}
	return nil
}

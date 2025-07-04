// Package authz provides authorization utilities using Cedar policies.
package authz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/exp/jsonrpc2"
)

// ResponseFilteringWriter wraps an http.ResponseWriter to intercept and filter responses
type ResponseFilteringWriter struct {
	http.ResponseWriter
	authorizer *CedarAuthorizer
	request    *http.Request
	method     string
	buffer     *bytes.Buffer
	statusCode int
}

// NewResponseFilteringWriter creates a new response filtering writer
func NewResponseFilteringWriter(
	w http.ResponseWriter, authorizer *CedarAuthorizer, r *http.Request, method string,
) *ResponseFilteringWriter {
	return &ResponseFilteringWriter{
		ResponseWriter: w,
		authorizer:     authorizer,
		request:        r,
		method:         method,
		buffer:         &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}
}

// Write captures the response body for filtering
func (rfw *ResponseFilteringWriter) Write(data []byte) (int, error) {
	return rfw.buffer.Write(data)
}

// WriteHeader captures the status code
func (rfw *ResponseFilteringWriter) WriteHeader(statusCode int) {
	rfw.statusCode = statusCode
}

// Flush processes the captured response and applies filtering if needed
func (rfw *ResponseFilteringWriter) Flush() error {
	// If it's not a successful response, just pass it through
	if rfw.statusCode != http.StatusOK && rfw.statusCode != http.StatusAccepted {
		rfw.ResponseWriter.WriteHeader(rfw.statusCode)
		_, err := rfw.ResponseWriter.Write(rfw.buffer.Bytes())
		return err
	}

	// Check if this is a list operation that needs filtering
	if !isListOperation(rfw.method) {
		rfw.ResponseWriter.WriteHeader(rfw.statusCode)
		_, err := rfw.ResponseWriter.Write(rfw.buffer.Bytes())
		return err
	}

	rawResponse := rfw.buffer.Bytes()

	// Skip filtering for empty responses (common in SSE scenarios where actual data comes via SSE stream)
	if len(rawResponse) == 0 {
		rfw.ResponseWriter.WriteHeader(rfw.statusCode)
		_, err := rfw.ResponseWriter.Write(rawResponse)
		return err
	}

	// Parse the JSON-RPC response
	var response jsonrpc2.Response
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		// If we can't parse it, just pass it through
		rfw.ResponseWriter.WriteHeader(rfw.statusCode)
		_, err := rfw.ResponseWriter.Write(rawResponse)
		return err
	}

	// Filter the response based on authorization
	filteredResponse, err := rfw.filterListResponse(&response)
	if err != nil {
		// If filtering fails, return an error response
		return rfw.writeErrorResponse(response.ID, err)
	}

	// Write the filtered response
	filteredData, err := json.Marshal(filteredResponse)
	if err != nil {
		return rfw.writeErrorResponse(response.ID, err)
	}

	rfw.ResponseWriter.WriteHeader(rfw.statusCode)
	_, err = rfw.ResponseWriter.Write(filteredData)
	return err
}

// isListOperation checks if the method is a list operation
func isListOperation(method string) bool {
	return method == string(mcp.MethodToolsList) ||
		method == string(mcp.MethodPromptsList) ||
		method == string(mcp.MethodResourcesList)
}

// filterListResponse filters the list response based on authorization policies
func (rfw *ResponseFilteringWriter) filterListResponse(response *jsonrpc2.Response) (*jsonrpc2.Response, error) {
	if response.Error != nil {
		// If there's an error in the response, don't filter
		return response, nil
	}

	if response.Result == nil {
		// If there's no result, don't filter
		return response, nil
	}

	// Filter based on the method
	switch rfw.method {
	case string(mcp.MethodToolsList):
		return rfw.filterToolsResponse(response)
	case string(mcp.MethodPromptsList):
		return rfw.filterPromptsResponse(response)
	case string(mcp.MethodResourcesList):
		return rfw.filterResourcesResponse(response)
	default:
		// Unknown list method, just return as-is
		return response, nil
	}
}

// filterToolsResponse filters tools based on call_tool authorization
func (rfw *ResponseFilteringWriter) filterToolsResponse(response *jsonrpc2.Response) (*jsonrpc2.Response, error) {
	// Parse the result as a ListToolsResult
	var listResult mcp.ListToolsResult
	if err := json.Unmarshal(response.Result, &listResult); err != nil {
		// If we can't parse it as a list response, just return it as-is
		return response, nil
	}

	var filteredTools []mcp.Tool

	for _, tool := range listResult.Tools {
		// Check if the user is authorized to call this tool
		authorized, err := rfw.authorizer.AuthorizeWithJWTClaims(
			rfw.request.Context(),
			MCPFeatureTool,
			MCPOperationCall,
			tool.Name,
			nil, // No arguments for the authorization check
		)
		if err != nil {
			// If there's an error checking authorization, skip this tool
			continue
		}

		if authorized {
			filteredTools = append(filteredTools, tool)
		}
	}

	// Create a new result with filtered tools
	filteredResult := mcp.ListToolsResult{
		PaginatedResult: listResult.PaginatedResult,
		Tools:           filteredTools,
	}

	// Marshal the filtered result back
	filteredResultData, err := json.Marshal(filteredResult)
	if err != nil {
		return nil, err
	}

	// Create a new response with the filtered result
	filteredResponse := &jsonrpc2.Response{
		ID:     response.ID,
		Result: json.RawMessage(filteredResultData),
	}

	return filteredResponse, nil
}

// filterPromptsResponse filters prompts based on get_prompt authorization
func (rfw *ResponseFilteringWriter) filterPromptsResponse(response *jsonrpc2.Response) (*jsonrpc2.Response, error) {
	// Parse the result as a ListPromptsResult
	var listResult mcp.ListPromptsResult
	if err := json.Unmarshal(response.Result, &listResult); err != nil {
		// If we can't parse it as a list response, just return it as-is
		return response, nil
	}

	var filteredPrompts []mcp.Prompt

	for _, prompt := range listResult.Prompts {
		// Check if the user is authorized to get this prompt
		authorized, err := rfw.authorizer.AuthorizeWithJWTClaims(
			rfw.request.Context(),
			MCPFeaturePrompt,
			MCPOperationGet,
			prompt.Name,
			nil, // No arguments for the authorization check
		)
		if err != nil {
			// If there's an error checking authorization, skip this prompt
			continue
		}

		if authorized {
			filteredPrompts = append(filteredPrompts, prompt)
		}
	}

	// Create a new result with filtered prompts
	filteredResult := mcp.ListPromptsResult{
		PaginatedResult: listResult.PaginatedResult,
		Prompts:         filteredPrompts,
	}

	// Marshal the filtered result back
	filteredResultData, err := json.Marshal(filteredResult)
	if err != nil {
		return nil, err
	}

	// Create a new response with the filtered result
	filteredResponse := &jsonrpc2.Response{
		ID:     response.ID,
		Result: json.RawMessage(filteredResultData),
	}

	return filteredResponse, nil
}

// filterResourcesResponse filters resources based on read_resource authorization
func (rfw *ResponseFilteringWriter) filterResourcesResponse(response *jsonrpc2.Response) (*jsonrpc2.Response, error) {
	// Parse the result as a ListResourcesResult
	var listResult mcp.ListResourcesResult
	if err := json.Unmarshal(response.Result, &listResult); err != nil {
		// If we can't parse it as a list response, just return it as-is
		return response, nil
	}

	var filteredResources []mcp.Resource

	for _, resource := range listResult.Resources {
		// Check if the user is authorized to read this resource
		authorized, err := rfw.authorizer.AuthorizeWithJWTClaims(
			rfw.request.Context(),
			MCPFeatureResource,
			MCPOperationRead,
			resource.URI,
			nil, // No arguments for the authorization check
		)
		if err != nil {
			// If there's an error checking authorization, skip this resource
			continue
		}

		if authorized {
			filteredResources = append(filteredResources, resource)
		}
	}

	// Create a new result with filtered resources
	filteredResult := mcp.ListResourcesResult{
		PaginatedResult: listResult.PaginatedResult,
		Resources:       filteredResources,
	}

	// Marshal the filtered result back
	filteredResultData, err := json.Marshal(filteredResult)
	if err != nil {
		return nil, err
	}

	// Create a new response with the filtered result
	filteredResponse := &jsonrpc2.Response{
		ID:     response.ID,
		Result: json.RawMessage(filteredResultData),
	}

	return filteredResponse, nil
}

// writeErrorResponse writes an error response
func (rfw *ResponseFilteringWriter) writeErrorResponse(id jsonrpc2.ID, err error) error {
	errorResponse := &jsonrpc2.Response{
		ID:    id,
		Error: jsonrpc2.NewError(500, fmt.Sprintf("Error filtering response: %v", err)),
	}

	errorData, marshalErr := json.Marshal(errorResponse)
	if marshalErr != nil {
		// If we can't even marshal the error, write a simple error
		rfw.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		_, writeErr := rfw.ResponseWriter.Write([]byte(`{"error": "Internal server error"}`))
		return writeErr
	}

	rfw.ResponseWriter.WriteHeader(http.StatusInternalServerError)
	_, writeErr := rfw.ResponseWriter.Write(errorData)
	return writeErr
}

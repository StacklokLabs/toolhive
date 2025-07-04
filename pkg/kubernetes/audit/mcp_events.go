// Package audit provides MCP-specific audit event types and constants.
package audit

// MCP-specific event types based on the Model Context Protocol specification
const (
	// EventTypeMCPInitialize represents an MCP initialization event
	EventTypeMCPInitialize = "mcp_initialize"
	// EventTypeMCPToolCall represents an MCP tool call event
	EventTypeMCPToolCall = "mcp_tool_call"
	// EventTypeMCPToolsList represents an MCP tools list event
	EventTypeMCPToolsList = "mcp_tools_list"
	// EventTypeMCPResourceRead represents an MCP resource read event
	EventTypeMCPResourceRead = "mcp_resource_read"
	// EventTypeMCPResourcesList represents an MCP resources list event
	EventTypeMCPResourcesList = "mcp_resources_list"
	// EventTypeMCPPromptGet represents an MCP prompt get event
	EventTypeMCPPromptGet = "mcp_prompt_get"
	// EventTypeMCPPromptsList represents an MCP prompts list event
	EventTypeMCPPromptsList = "mcp_prompts_list"
	// EventTypeMCPNotification represents an MCP notification event
	EventTypeMCPNotification = "mcp_notification"
	// EventTypeMCPPing represents an MCP ping event
	EventTypeMCPPing = "mcp_ping"
	// EventTypeMCPLogging represents an MCP logging event
	EventTypeMCPLogging = "mcp_logging"
	// EventTypeMCPCompletion represents an MCP completion event
	EventTypeMCPCompletion = "mcp_completion"
	// EventTypeMCPRootsListChanged represents an MCP roots list changed notification
	EventTypeMCPRootsListChanged = "mcp_roots_list_changed"

	// Fallback event types for unrecognized or generic requests
	// EventTypeMCPRequest represents a generic MCP request when specific type cannot be determined
	EventTypeMCPRequest = "mcp_request"
	// EventTypeHTTPRequest represents a generic HTTP request (non-MCP)
	EventTypeHTTPRequest = "http_request"
)

// MCP target types for audit events
const (
	// TargetTypeTool represents a tool target
	TargetTypeTool = "tool"
	// TargetTypeResource represents a resource target
	TargetTypeResource = "resource"
	// TargetTypePrompt represents a prompt target
	TargetTypePrompt = "prompt"
	// TargetTypeServer represents a server target
	TargetTypeServer = "server"
)

// MCP-specific target field keys
const (
	// TargetKeyType is the key for the target type in the target map
	TargetKeyType = "type"
	// TargetKeyName is the key for the target name in the target map
	TargetKeyName = "name"
	// TargetKeyURI is the key for the target URI in the target map
	TargetKeyURI = "uri"
	// TargetKeyMethod is the key for the MCP method in the target map
	TargetKeyMethod = "method"
	// TargetKeyEndpoint is the key for the endpoint in the target map
	TargetKeyEndpoint = "endpoint"
)

// MCP-specific subject field keys
const (
	// SubjectKeyUser is the key for the user in the subjects map
	SubjectKeyUser = "user"
	// SubjectKeyUserID is the key for the user ID in the subjects map
	SubjectKeyUserID = "user_id"
	// SubjectKeyClientName is the key for the client name in the subjects map
	SubjectKeyClientName = "client_name"
	// SubjectKeyClientVersion is the key for the client version in the subjects map
	SubjectKeyClientVersion = "client_version"
)

// MCP-specific source field keys for EventSource.Extra
const (
	// SourceExtraKeyUserAgent is the key for the user agent in the source extra map
	SourceExtraKeyUserAgent = "user_agent"
	// SourceExtraKeyRequestID is the key for the request ID in the source extra map
	SourceExtraKeyRequestID = "request_id"
	// SourceExtraKeySessionID is the key for the session ID in the source extra map
	SourceExtraKeySessionID = "session_id"
)

// MCP-specific metadata field keys for EventMetadata.Extra
const (
	// MetadataExtraKeyMCPVersion is the key for the MCP version in the metadata extra map
	MetadataExtraKeyMCPVersion = "mcp_version"
	// MetadataExtraKeyTransport is the key for the transport type in the metadata extra map
	MetadataExtraKeyTransport = "transport"
	// MetadataExtraKeyDuration is the key for the request duration in the metadata extra map
	MetadataExtraKeyDuration = "duration_ms"
	// MetadataExtraKeyResponseSize is the key for the response size in the metadata extra map
	MetadataExtraKeyResponseSize = "response_size_bytes"
)

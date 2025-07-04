// Package audit provides audit logging functionality for ToolHive.
package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/mcp"
)

// LevelAudit is a custom audit log level - between Info and Warn
const LevelAudit = slog.Level(2)

// NewAuditLogger creates a new structured audit logger that writes to the specified writer.
func NewAuditLogger(w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: LevelAudit,
	})

	return slog.New(handler)
}

// Auditor handles audit logging for HTTP requests.
type Auditor struct {
	config      *Config
	auditLogger *slog.Logger
}

// NewAuditor creates a new Auditor with the given configuration.
func NewAuditor(config *Config) (*Auditor, error) {
	var logWriter io.Writer = os.Stdout // default to stdout

	if config != nil {
		w, err := config.GetLogWriter()
		if err != nil {
			// Log error and fall back to stdout
			logger.Errorf("Failed to open audit log file, falling back to stdout: %v", err)
			return nil, err
		}
		logWriter = w
	}

	return &Auditor{
		config:      config,
		auditLogger: NewAuditLogger(logWriter),
	}, nil
}

// responseWriter wraps http.ResponseWriter to capture response data and status.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
	auditor    *Auditor
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	// Capture response data if configured
	if rw.auditor.config.IncludeResponseData && rw.body != nil {
		// Limit the size of captured data
		if rw.body.Len()+len(data) <= rw.auditor.config.MaxDataSize {
			rw.body.Write(data)
		}
	}
	return rw.ResponseWriter.Write(data)
}

// Middleware creates an HTTP middleware that logs audit events.
func (a *Auditor) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle SSE endpoints specially - log the connection event immediately
		// since SSE connections are long-lived and don't follow normal request/response pattern
		if r.URL.Path == "/sse" {
			// Log SSE connection event immediately
			a.logSSEConnectionEvent(r)

			// Pass through to SSE handler without waiting
			next.ServeHTTP(w, r)
			return
		}

		startTime := time.Now()

		// Capture request data if configured
		var requestData []byte
		if a.config.IncludeRequestData && r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err == nil && len(body) <= a.config.MaxDataSize {
				requestData = body
				// Restore the body for the next handler
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
		}

		// Wrap the response writer to capture response data and status
		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default status
			auditor:        a,
		}

		if a.config.IncludeResponseData {
			rw.body = &bytes.Buffer{}
		}

		// Process the request
		next.ServeHTTP(rw, r)

		// Calculate duration
		duration := time.Since(startTime)

		// Create and log the audit event
		a.logAuditEvent(r, rw, requestData, duration)
	})
}

// logAuditEvent creates and logs an audit event for the HTTP request.
func (a *Auditor) logAuditEvent(r *http.Request, rw *responseWriter, requestData []byte, duration time.Duration) {
	// Determine event type based on the request
	eventType := a.determineEventType(r)

	// Determine outcome based on status code
	outcome := a.determineOutcome(rw.statusCode)

	// Check if we should audit this event
	if !a.config.ShouldAuditEvent(eventType) {
		return
	}

	// Extract source information
	source := a.extractSource(r)

	// Extract subject information
	subjects := a.extractSubjects(r)

	// Determine component name
	component := a.determineComponent(r)

	// Create the audit event
	event := NewAuditEvent(eventType, source, outcome, subjects, component)

	// Add target information
	target := a.extractTarget(r, eventType)
	if len(target) > 0 {
		event.WithTarget(target)
	}

	// Add metadata
	a.addMetadata(event, r, duration, rw)

	// Add request/response data if configured
	a.addEventData(event, r, rw, requestData)

	// Log the audit event
	event.LogTo(r.Context(), a.auditLogger, LevelAudit)
}

// determineEventType determines the event type based on the HTTP request.
func (a *Auditor) determineEventType(r *http.Request) string {
	// First, try to get the parsed MCP method from context
	if mcpMethod := mcp.GetMCPMethod(r.Context()); mcpMethod != "" {
		return a.mapMCPMethodToEventType(mcpMethod)
	}

	// Fall back to path-based detection for non-MCP requests
	path := r.URL.Path

	// Handle SSE connection establishment
	if strings.Contains(path, "/sse") {
		return EventTypeMCPInitialize
	}

	// Handle MCP message endpoints that weren't parsed (malformed requests)
	if strings.Contains(path, "/messages") && r.Method == "POST" {
		return EventTypeMCPRequest
	}

	// Default for non-MCP requests
	return EventTypeHTTPRequest
}

// mapMCPMethodToEventType maps MCP method names to event types.
func (*Auditor) mapMCPMethodToEventType(mcpMethod string) string {
	switch mcpMethod {
	case "initialize":
		return EventTypeMCPInitialize
	case "tools/call":
		return EventTypeMCPToolCall
	case "tools/list":
		return EventTypeMCPToolsList
	case "resources/read":
		return EventTypeMCPResourceRead
	case "resources/list":
		return EventTypeMCPResourcesList
	case "prompts/get":
		return EventTypeMCPPromptGet
	case "prompts/list":
		return EventTypeMCPPromptsList
	case "notifications/message":
		return EventTypeMCPNotification
	case "ping":
		return EventTypeMCPPing
	case "logging/setLevel":
		return EventTypeMCPLogging
	case "completion/complete":
		return EventTypeMCPCompletion
	case "notifications/roots/list_changed":
		return EventTypeMCPRootsListChanged
	default:
		return EventTypeMCPRequest
	}
}

// determineOutcome determines the outcome based on the HTTP status code.
func (*Auditor) determineOutcome(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return OutcomeSuccess
	case statusCode == 401 || statusCode == 403:
		return OutcomeDenied
	case statusCode >= 400 && statusCode < 500:
		return OutcomeFailure
	case statusCode >= 500:
		return OutcomeError
	default:
		return OutcomeSuccess
	}
}

// extractSource extracts source information from the HTTP request.
func (a *Auditor) extractSource(r *http.Request) EventSource {
	// Get the client IP address
	clientIP := a.getClientIP(r)

	source := EventSource{
		Type:  SourceTypeNetwork,
		Value: clientIP,
		Extra: make(map[string]any),
	}

	// Add user agent if available
	if userAgent := r.Header.Get("User-Agent"); userAgent != "" {
		source.Extra[SourceExtraKeyUserAgent] = userAgent
	}

	// Add request ID if available
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		source.Extra[SourceExtraKeyRequestID] = requestID
	}

	return source
}

// getClientIP extracts the client IP address from the request.
func (*Auditor) getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		if ips := strings.Split(xff, ","); len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

// extractSubjects extracts subject information from the HTTP request.
func (*Auditor) extractSubjects(r *http.Request) map[string]string {
	subjects := make(map[string]string)

	// Extract user information from JWT claims
	if claims, ok := auth.GetClaimsFromContext(r.Context()); ok {
		if sub, ok := claims["sub"].(string); ok && sub != "" {
			subjects[SubjectKeyUserID] = sub
		}

		if name, ok := claims["name"].(string); ok && name != "" {
			subjects[SubjectKeyUser] = name
		} else if preferredUsername, ok := claims["preferred_username"].(string); ok && preferredUsername != "" {
			subjects[SubjectKeyUser] = preferredUsername
		} else if email, ok := claims["email"].(string); ok && email != "" {
			subjects[SubjectKeyUser] = email
		}

		// Add client information if available
		if clientName, ok := claims["client_name"].(string); ok && clientName != "" {
			subjects[SubjectKeyClientName] = clientName
		}

		if clientVersion, ok := claims["client_version"].(string); ok && clientVersion != "" {
			subjects[SubjectKeyClientVersion] = clientVersion
		}
	}

	// If no user found in claims, set anonymous
	if subjects[SubjectKeyUser] == "" {
		subjects[SubjectKeyUser] = "anonymous"
	}

	return subjects
}

// determineComponent determines the component name based on the request.
func (a *Auditor) determineComponent(_ *http.Request) string {
	// Use the component from configuration if set
	if a.config.Component != "" {
		return a.config.Component
	}
	// For MCP requests, we could extract the server name from the path or headers
	// For now, we'll use a default component name
	return ComponentToolHive
}

// extractTarget extracts target information from the HTTP request.
func (*Auditor) extractTarget(r *http.Request, eventType string) map[string]string {
	target := make(map[string]string)

	target[TargetKeyEndpoint] = r.URL.Path
	target[TargetKeyMethod] = r.Method

	// Add MCP method if available from parsed data
	if mcpMethod := mcp.GetMCPMethod(r.Context()); mcpMethod != "" {
		target[TargetKeyMethod] = mcpMethod
	}

	// Add resource ID if available from parsed data
	if resourceID := mcp.GetMCPResourceID(r.Context()); resourceID != "" {
		target[TargetKeyName] = resourceID
	}

	// Add event-specific target information
	switch eventType {
	case EventTypeMCPToolCall:
		target[TargetKeyType] = TargetTypeTool
	case EventTypeMCPResourceRead:
		target[TargetKeyType] = TargetTypeResource
	case EventTypeMCPPromptGet:
		target[TargetKeyType] = TargetTypePrompt
	default:
		target[TargetKeyType] = "endpoint"
	}

	return target
}

// addMetadata adds metadata to the audit event.
func (*Auditor) addMetadata(event *AuditEvent, r *http.Request, duration time.Duration, rw *responseWriter) {
	if event.Metadata.Extra == nil {
		event.Metadata.Extra = make(map[string]any)
	}

	// Add duration
	event.Metadata.Extra[MetadataExtraKeyDuration] = duration.Milliseconds()

	// Add transport information
	if strings.Contains(r.URL.Path, "/sse") {
		event.Metadata.Extra[MetadataExtraKeyTransport] = "sse"
	} else {
		event.Metadata.Extra[MetadataExtraKeyTransport] = "http"
	}

	// Add response size if available
	if rw.body != nil {
		event.Metadata.Extra[MetadataExtraKeyResponseSize] = rw.body.Len()
	}
}

// addEventData adds request/response data to the audit event if configured.
func (a *Auditor) addEventData(event *AuditEvent, _ *http.Request, rw *responseWriter, requestData []byte) {
	if !a.config.IncludeRequestData && !a.config.IncludeResponseData {
		return
	}

	data := make(map[string]any)

	if a.config.IncludeRequestData && len(requestData) > 0 {
		// Try to parse as JSON, otherwise store as string
		var requestJSON any
		if err := json.Unmarshal(requestData, &requestJSON); err == nil {
			data["request"] = requestJSON
		} else {
			data["request"] = string(requestData)
		}
	}

	if a.config.IncludeResponseData && rw.body != nil && rw.body.Len() > 0 {
		responseData := rw.body.Bytes()
		// Try to parse as JSON, otherwise store as string
		var responseJSON any
		if err := json.Unmarshal(responseData, &responseJSON); err == nil {
			data["response"] = responseJSON
		} else {
			data["response"] = string(responseData)
		}
	}

	if len(data) > 0 {
		if dataBytes, err := json.Marshal(data); err == nil {
			rawMsg := json.RawMessage(dataBytes)
			event.WithData(&rawMsg)
		}
	}
}

// logSSEConnectionEvent logs an audit event for SSE connection initiation.
func (a *Auditor) logSSEConnectionEvent(r *http.Request) {
	// Extract source information
	source := a.extractSource(r)

	// Extract subject information
	subjects := a.extractSubjects(r)

	// Determine component name
	component := a.determineComponent(r)

	// Create the audit event for SSE connection
	event := NewAuditEvent("sse_connection", source, OutcomeSuccess, subjects, component)

	// Add target information
	target := map[string]string{
		"endpoint": r.URL.Path,
		"method":   r.Method,
		"type":     "sse_endpoint",
	}
	event.WithTarget(target)

	// Add metadata
	event.Metadata.Extra = map[string]any{
		"transport":  "sse",
		"user_agent": r.Header.Get("User-Agent"),
	}

	// Log the event
	event.LogTo(r.Context(), a.auditLogger, LevelAudit)
}

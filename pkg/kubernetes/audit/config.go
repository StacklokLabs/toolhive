// Package audit provides audit logging configuration for ToolHive.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Config represents the audit logging configuration.
type Config struct {
	// Component is the component name to use in audit events
	Component string `json:"component,omitempty" yaml:"component,omitempty"`
	// EventTypes specifies which event types to audit. If empty, all events are audited.
	EventTypes []string `json:"event_types,omitempty" yaml:"event_types,omitempty"`
	// ExcludeEventTypes specifies which event types to exclude from auditing.
	// This takes precedence over EventTypes.
	ExcludeEventTypes []string `json:"exclude_event_types,omitempty" yaml:"exclude_event_types,omitempty"`
	// IncludeRequestData determines whether to include request data in audit logs
	IncludeRequestData bool `json:"include_request_data,omitempty" yaml:"include_request_data,omitempty"`
	// IncludeResponseData determines whether to include response data in audit logs
	IncludeResponseData bool `json:"include_response_data,omitempty" yaml:"include_response_data,omitempty"`
	// MaxDataSize limits the size of request/response data included in audit logs (in bytes)
	MaxDataSize int `json:"max_data_size,omitempty" yaml:"max_data_size,omitempty"`
	// LogFile specifies the file path for audit logs. If empty, logs to stdout.
	LogFile string `json:"log_file,omitempty" yaml:"log_file,omitempty"`
}

// GetLogWriter creates and returns the appropriate io.Writer based on the configuration.
func (c *Config) GetLogWriter() (io.Writer, error) {
	if c == nil || c.LogFile == "" {
		return os.Stdout, nil
	}

	// Clean the path to prevent directory traversal
	file, err := os.OpenFile(filepath.Clean(c.LogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log file %s: %w", c.LogFile, err)
	}

	return file, nil
}

// DefaultConfig returns a default audit configuration.
func DefaultConfig() *Config {
	return &Config{
		IncludeRequestData:  false, // Disabled by default for privacy
		IncludeResponseData: false, // Disabled by default for privacy
		MaxDataSize:         1024,  // 1KB default limit
	}
}

// LoadFromFile loads audit configuration from a file.
func LoadFromFile(path string) (*Config, error) {
	// Clean the path to prevent directory traversal
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open audit config file: %w", err)
	}
	defer file.Close()

	return LoadFromReader(file)
}

// LoadFromReader loads audit configuration from an io.Reader.
func LoadFromReader(r io.Reader) (*Config, error) {
	var config Config
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode audit config: %w", err)
	}

	return &config, nil
}

// ShouldAuditEvent determines whether an event should be audited based on the configuration.
func (c *Config) ShouldAuditEvent(eventType string) bool {
	// Check if event type is excluded
	for _, excludeType := range c.ExcludeEventTypes {
		if excludeType == eventType {
			return false
		}
	}

	// If specific event types are configured, check if this event type is included
	if len(c.EventTypes) > 0 {
		found := false
		for _, allowedType := range c.EventTypes {
			if allowedType == eventType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// CreateMiddleware creates an HTTP middleware from the audit configuration.
func (c *Config) CreateMiddleware() (func(http.Handler) http.Handler, error) {
	auditor, err := NewAuditor(c)
	if err != nil {
		return nil, fmt.Errorf("failed to create auditor: %w", err)
	}
	return auditor.Middleware, nil
}

// GetMiddlewareFromFile loads the audit configuration from a file and creates an HTTP middleware.
func GetMiddlewareFromFile(path string) (func(http.Handler) http.Handler, error) {
	// Load the configuration
	config, err := LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load audit config: %w", err)
	}

	// Create the middleware
	return config.CreateMiddleware()
}

// Validate validates the audit configuration.
func (c *Config) Validate() error {
	if c.MaxDataSize < 0 {
		return fmt.Errorf("max_data_size cannot be negative")
	}

	// Validate event types (basic validation - could be extended)
	validEventTypes := map[string]bool{
		EventTypeMCPInitialize:       true,
		EventTypeMCPToolCall:         true,
		EventTypeMCPToolsList:        true,
		EventTypeMCPResourceRead:     true,
		EventTypeMCPResourcesList:    true,
		EventTypeMCPPromptGet:        true,
		EventTypeMCPPromptsList:      true,
		EventTypeMCPNotification:     true,
		EventTypeMCPPing:             true,
		EventTypeMCPLogging:          true,
		EventTypeMCPCompletion:       true,
		EventTypeMCPRootsListChanged: true,
		// Fallback event types that can also be emitted by the middleware
		EventTypeMCPRequest:  true,
		EventTypeHTTPRequest: true,
	}

	for _, eventType := range c.EventTypes {
		if !validEventTypes[eventType] {
			return fmt.Errorf("unknown event type: %s", eventType)
		}
	}

	for _, eventType := range c.ExcludeEventTypes {
		if !validEventTypes[eventType] {
			return fmt.Errorf("unknown exclude event type: %s", eventType)
		}
	}

	return nil
}

package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()

	assert.False(t, config.IncludeRequestData)
	assert.False(t, config.IncludeResponseData)
	assert.Equal(t, 1024, config.MaxDataSize)
	assert.Empty(t, config.Component)
	assert.Empty(t, config.EventTypes)
	assert.Empty(t, config.ExcludeEventTypes)
}

func TestLoadFromReader(t *testing.T) {
	t.Parallel()
	jsonConfig := `{
		"component": "test-component",
		"event_types": ["mcp_tool_call", "mcp_resource_read"],
		"exclude_event_types": ["mcp_ping"],
		"include_request_data": true,
		"include_response_data": false,
		"max_data_size": 2048
	}`

	config, err := LoadFromReader(strings.NewReader(jsonConfig))
	require.NoError(t, err)

	assert.Equal(t, "test-component", config.Component)
	assert.Equal(t, []string{"mcp_tool_call", "mcp_resource_read"}, config.EventTypes)
	assert.Equal(t, []string{"mcp_ping"}, config.ExcludeEventTypes)
	assert.True(t, config.IncludeRequestData)
	assert.False(t, config.IncludeResponseData)
	assert.Equal(t, 2048, config.MaxDataSize)
}

func TestLoadFromReaderInvalidJSON(t *testing.T) {
	t.Parallel()
	invalidJSON := `{"invalid": }`

	_, err := LoadFromReader(strings.NewReader(invalidJSON))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode audit config")
}

func TestShouldAuditEventAllEventsAllowed(t *testing.T) {
	t.Parallel()
	config := &Config{}

	result := config.ShouldAuditEvent("any_event")
	assert.True(t, result)
}

func TestShouldAuditEventAllEventsEnabled(t *testing.T) {
	t.Parallel()
	config := &Config{
		// No EventTypes specified, so all events should be audited
	}

	assert.True(t, config.ShouldAuditEvent("mcp_tool_call"))
	assert.True(t, config.ShouldAuditEvent("mcp_resource_read"))
	assert.True(t, config.ShouldAuditEvent("custom_event"))
}

func TestShouldAuditEventSpecificTypes(t *testing.T) {
	t.Parallel()
	config := &Config{
		EventTypes: []string{"mcp_tool_call", "mcp_resource_read"},
	}

	assert.True(t, config.ShouldAuditEvent("mcp_tool_call"))
	assert.True(t, config.ShouldAuditEvent("mcp_resource_read"))
	assert.False(t, config.ShouldAuditEvent("mcp_ping"))
	assert.False(t, config.ShouldAuditEvent("custom_event"))
}

func TestShouldAuditEventExcludeTypes(t *testing.T) {
	t.Parallel()
	config := &Config{
		ExcludeEventTypes: []string{"mcp_ping", "mcp_logging"},
	}

	assert.True(t, config.ShouldAuditEvent("mcp_tool_call"))
	assert.True(t, config.ShouldAuditEvent("mcp_resource_read"))
	assert.False(t, config.ShouldAuditEvent("mcp_ping"))
	assert.False(t, config.ShouldAuditEvent("mcp_logging"))
}

func TestShouldAuditEventExcludeTakesPrecedence(t *testing.T) {
	t.Parallel()
	config := &Config{
		EventTypes:        []string{"mcp_tool_call", "mcp_ping"},
		ExcludeEventTypes: []string{"mcp_ping"},
	}

	assert.True(t, config.ShouldAuditEvent("mcp_tool_call"))
	assert.False(t, config.ShouldAuditEvent("mcp_ping"))          // Excluded despite being in EventTypes
	assert.False(t, config.ShouldAuditEvent("mcp_resource_read")) // Not in EventTypes
}

func TestCreateMiddleware(t *testing.T) {
	t.Parallel()
	config := &Config{}

	middleware, err := config.CreateMiddleware()
	assert.NoError(t, err)
	assert.NotNil(t, middleware)
}

func TestValidateValidConfig(t *testing.T) {
	t.Parallel()
	config := &Config{
		EventTypes:          []string{EventTypeMCPToolCall, EventTypeMCPResourceRead},
		ExcludeEventTypes:   []string{EventTypeMCPPing},
		IncludeRequestData:  true,
		IncludeResponseData: false,
		MaxDataSize:         2048,
	}

	err := config.Validate()
	assert.NoError(t, err)
}

func TestValidateNegativeMaxDataSize(t *testing.T) {
	t.Parallel()
	config := &Config{
		MaxDataSize: -1,
	}

	err := config.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max_data_size cannot be negative")
}

func TestValidateInvalidEventType(t *testing.T) {
	t.Parallel()
	config := &Config{
		EventTypes: []string{"invalid_event_type"},
	}

	err := config.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown event type: invalid_event_type")
}

func TestValidateInvalidExcludeEventType(t *testing.T) {
	t.Parallel()
	config := &Config{
		ExcludeEventTypes: []string{"invalid_exclude_type"},
	}

	err := config.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown exclude event type: invalid_exclude_type")
}

func TestValidateAllValidEventTypes(t *testing.T) {
	t.Parallel()
	validEventTypes := []string{
		EventTypeMCPInitialize,
		EventTypeMCPToolCall,
		EventTypeMCPToolsList,
		EventTypeMCPResourceRead,
		EventTypeMCPResourcesList,
		EventTypeMCPPromptGet,
		EventTypeMCPPromptsList,
		EventTypeMCPNotification,
		EventTypeMCPPing,
		EventTypeMCPLogging,
		EventTypeMCPCompletion,
		EventTypeMCPRootsListChanged,
	}

	config := &Config{
		EventTypes: validEventTypes,
	}

	err := config.Validate()
	assert.NoError(t, err)
}

func TestConfigJSONSerialization(t *testing.T) {
	t.Parallel()
	originalConfig := &Config{
		Component:           "test-service",
		EventTypes:          []string{EventTypeMCPToolCall, EventTypeMCPResourceRead},
		ExcludeEventTypes:   []string{EventTypeMCPPing},
		IncludeRequestData:  true,
		IncludeResponseData: false,
		MaxDataSize:         4096,
	}

	// Serialize to JSON
	jsonData, err := json.Marshal(originalConfig)
	require.NoError(t, err)

	// Deserialize back
	var deserializedConfig Config
	err = json.Unmarshal(jsonData, &deserializedConfig)
	require.NoError(t, err)

	// Verify all fields are preserved
	assert.Equal(t, originalConfig.Component, deserializedConfig.Component)
	assert.Equal(t, originalConfig.EventTypes, deserializedConfig.EventTypes)
	assert.Equal(t, originalConfig.ExcludeEventTypes, deserializedConfig.ExcludeEventTypes)
	assert.Equal(t, originalConfig.IncludeRequestData, deserializedConfig.IncludeRequestData)
	assert.Equal(t, originalConfig.IncludeResponseData, deserializedConfig.IncludeResponseData)
	assert.Equal(t, originalConfig.MaxDataSize, deserializedConfig.MaxDataSize)
}

func TestConfigMinimalJSON(t *testing.T) {
	t.Parallel()
	minimalJSON := `{}`

	config, err := LoadFromReader(strings.NewReader(minimalJSON))
	require.NoError(t, err)

	assert.Empty(t, config.Component)
	assert.Empty(t, config.EventTypes)
	assert.Empty(t, config.ExcludeEventTypes)
	assert.False(t, config.IncludeRequestData)
	assert.False(t, config.IncludeResponseData)
	assert.Equal(t, 0, config.MaxDataSize) // Default zero value
}

func TestGetMiddlewareFromFileError(t *testing.T) {
	t.Parallel()
	// Test with non-existent file
	_, err := GetMiddlewareFromFile("/non/existent/file.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load audit config")
}

func TestLoadFromFilePathCleaning(t *testing.T) {
	t.Parallel()
	// Test that filepath.Clean is used (this is more of a smoke test)
	// We can't easily test the actual cleaning without creating files
	_, err := LoadFromFile("./non-existent-file.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open audit config file")
}

func TestConfigWithEmptyEventTypes(t *testing.T) {
	t.Parallel()
	config := &Config{
		EventTypes: []string{}, // Explicitly empty
	}

	// Should audit all events when EventTypes is empty
	assert.True(t, config.ShouldAuditEvent("any_event"))
	assert.True(t, config.ShouldAuditEvent("mcp_tool_call"))
}

func TestConfigWithEmptyExcludeEventTypes(t *testing.T) {
	t.Parallel()
	config := &Config{
		ExcludeEventTypes: []string{}, // Explicitly empty
	}

	// Should audit all events when ExcludeEventTypes is empty
	assert.True(t, config.ShouldAuditEvent("any_event"))
	assert.True(t, config.ShouldAuditEvent("mcp_tool_call"))
}

func TestGetLogWriter(t *testing.T) {
	t.Parallel()

	t.Run("default to stdout", func(t *testing.T) {
		t.Parallel()
		config := &Config{}

		writer, err := config.GetLogWriter()
		assert.NoError(t, err)
		assert.Equal(t, os.Stdout, writer)
	})

	t.Run("nil config defaults to stdout", func(t *testing.T) {
		t.Parallel()
		var config *Config

		writer, err := config.GetLogWriter()
		assert.NoError(t, err)
		assert.Equal(t, os.Stdout, writer)
	})

	t.Run("empty log file defaults to stdout", func(t *testing.T) {
		t.Parallel()
		config := &Config{LogFile: ""}

		writer, err := config.GetLogWriter()
		assert.NoError(t, err)
		assert.Equal(t, os.Stdout, writer)
	})

	t.Run("invalid log file path returns error", func(t *testing.T) {
		t.Parallel()
		config := &Config{LogFile: "/invalid/path/that/does/not/exist/audit.log"}

		_, err := config.GetLogWriter()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to open audit log file")
	})
}

func TestConfigWithLogFile(t *testing.T) {
	t.Parallel()
	jsonConfig := `{
		"component": "test-component",
		"log_file": "/tmp/audit.log",
		"include_request_data": true
	}`

	config, err := LoadFromReader(strings.NewReader(jsonConfig))
	require.NoError(t, err)

	assert.Equal(t, "test-component", config.Component)
	assert.Equal(t, "/tmp/audit.log", config.LogFile)
	assert.True(t, config.IncludeRequestData)
}

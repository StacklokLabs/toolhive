package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	mcpparser "github.com/stacklok/toolhive/pkg/kubernetes/mcp"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	// Create a temporary file with a valid configuration
	tempFile, err := os.CreateTemp("", "authz-config-*.json")
	require.NoError(t, err, "Failed to create temporary file")
	defer os.Remove(tempFile.Name())

	// Create a valid configuration
	config := Config{
		Version: "1.0",
		Type:    ConfigTypeCedarV1,
		Cedar: &CedarConfig{
			Policies: []string{
				`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
			},
			EntitiesJSON: "[]",
		},
	}

	// Marshal the configuration to JSON
	configJSON, err := json.MarshalIndent(config, "", "  ")
	require.NoError(t, err, "Failed to marshal configuration to JSON")

	// Write the configuration to the temporary file
	_, err = tempFile.Write(configJSON)
	require.NoError(t, err, "Failed to write configuration to temporary file")
	tempFile.Close()

	// Load the configuration from the temporary file
	loadedConfig, err := LoadConfig(tempFile.Name())
	require.NoError(t, err, "Failed to load configuration from file")

	// Check if the loaded configuration matches the original configuration
	assert.Equal(t, config.Version, loadedConfig.Version, "Version does not match")
	assert.Equal(t, config.Type, loadedConfig.Type, "Type does not match")
	assert.Equal(t, config.Cedar.Policies, loadedConfig.Cedar.Policies, "Policies do not match")
	assert.Equal(t, config.Cedar.EntitiesJSON, loadedConfig.Cedar.EntitiesJSON, "EntitiesJSON does not match")
}

func TestValidateConfig(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		config      *Config
		expectError bool
	}{
		{
			name: "Valid configuration",
			config: &Config{
				Version: "1.0",
				Type:    ConfigTypeCedarV1,
				Cedar: &CedarConfig{
					Policies: []string{
						`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
					},
					EntitiesJSON: "[]",
				},
			},
			expectError: false,
		},
		{
			name: "Missing version",
			config: &Config{
				Type: ConfigTypeCedarV1,
				Cedar: &CedarConfig{
					Policies: []string{
						`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
					},
					EntitiesJSON: "[]",
				},
			},
			expectError: true,
		},
		{
			name: "Missing type",
			config: &Config{
				Version: "1.0",
				Cedar: &CedarConfig{
					Policies: []string{
						`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
					},
					EntitiesJSON: "[]",
				},
			},
			expectError: true,
		},
		{
			name: "Unsupported type",
			config: &Config{
				Version: "1.0",
				Type:    "unsupported",
				Cedar: &CedarConfig{
					Policies: []string{
						`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
					},
					EntitiesJSON: "[]",
				},
			},
			expectError: true,
		},
		{
			name: "Missing Cedar configuration",
			config: &Config{
				Version: "1.0",
				Type:    ConfigTypeCedarV1,
			},
			expectError: true,
		},
		{
			name: "Empty policies",
			config: &Config{
				Version: "1.0",
				Type:    ConfigTypeCedarV1,
				Cedar: &CedarConfig{
					Policies:     []string{},
					EntitiesJSON: "[]",
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.config.Validate()
			if tc.expectError {
				assert.Error(t, err, "Expected an error but got none")
			} else {
				assert.NoError(t, err, "Expected no error but got one")
			}
		})
	}
}

func TestCreateMiddleware(t *testing.T) {
	t.Parallel()
	// Create a valid configuration
	config := &Config{
		Version: "1.0",
		Type:    ConfigTypeCedarV1,
		Cedar: &CedarConfig{
			Policies: []string{
				`permit(principal, action == Action::"call_tool", resource == Tool::"weather");`,
			},
			EntitiesJSON: "[]",
		},
	}

	// Create the middleware
	middleware, err := config.CreateMiddleware()
	require.NoError(t, err, "Failed to create middleware")
	require.NotNil(t, middleware, "Middleware is nil")

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Apply the middleware chain: MCP parsing first, then authorization
	handler := mcpparser.ParsingMiddleware(middleware(testHandler))
	require.NotNil(t, handler, "Handler is nil")

	// Create a test request with a valid JSON-RPC message
	jsonRPCMessage := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "ping",
		"params":  map[string]interface{}{},
	}
	jsonRPCMessageBytes, err := json.Marshal(jsonRPCMessage)
	require.NoError(t, err, "Failed to marshal JSON-RPC message")

	req, err := http.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(jsonRPCMessageBytes))
	require.NoError(t, err, "Failed to create request")
	req.Header.Set("Content-Type", "application/json")

	// Add JWT claims to the request context
	claims := map[string]interface{}{
		"sub":  "user123",
		"name": "John Doe",
	}
	ctx := context.WithValue(req.Context(), auth.ClaimsContextKey{}, claims)
	req = req.WithContext(ctx)

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Serve the request
	handler.ServeHTTP(rr, req)

	// Check the response
	assert.Equal(t, http.StatusOK, rr.Code, "Response status code does not match expected")
}

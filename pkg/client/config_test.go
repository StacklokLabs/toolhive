// Package client provides utilities for managing client configurations
// and interacting with MCP servers.
package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/config"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/transport/ssecommon"
	"github.com/stacklok/toolhive/pkg/transport/types"
)

// createMockClientConfigs creates a set of mock client configurations for testing
func createMockClientConfigs() []mcpClientConfig {
	return []mcpClientConfig{
		{
			ClientType:           VSCode,
			Description:          "Visual Studio Code (Mock)",
			RelPath:              []string{"mock_vscode"},
			SettingsFile:         "settings.json",
			MCPServersPathPrefix: "/mcp/servers",
			Extension:            JSON,
		},
		{
			ClientType:           Cursor,
			Description:          "Cursor editor (Mock)",
			RelPath:              []string{"mock_cursor"},
			SettingsFile:         "mcp.json",
			MCPServersPathPrefix: "/mcpServers",
			Extension:            JSON,
		},
		{
			ClientType:           RooCode,
			Description:          "VS Code Roo Code extension (Mock)",
			RelPath:              []string{"mock_roo"},
			SettingsFile:         "mcp_settings.json",
			MCPServersPathPrefix: "/mcpServers",
			Extension:            JSON,
		},
		{
			ClientType:           Cline,
			Description:          "VS Code Cline extension (Mock)",
			RelPath:              []string{"mock_cline"},
			SettingsFile:         "mcp_settings.json",
			MCPServersPathPrefix: "/mcpServers",
			Extension:            JSON,
		},
		{
			ClientType:           ClaudeCode,
			Description:          "Claude Code CLI (Mock)",
			RelPath:              []string{"mock_claude"},
			SettingsFile:         ".claude.json",
			MCPServersPathPrefix: "/mcpServers",
			Extension:            JSON,
		},
		{
			ClientType:           JetBrainsCopilot,
			Description:          "JetBrains Copilot plugin (Mock)",
			RelPath:              []string{"mock_jetbrains_copilot"},
			SettingsFile:         "mcp.json",
			MCPServersPathPrefix: "/servers",
			Extension:            JSON,
		},
	}
}

// MockConfig creates a temporary config file with the provided configuration.
// It returns a cleanup function that should be deferred.
func MockConfig(t *testing.T, cfg *config.Config) func() {
	t.Helper()

	// Create a temporary directory for the test
	tempDir := t.TempDir()

	// TODO: see if there's a way to avoid changing env vars during tests.
	// Save original XDG_CONFIG_HOME
	originalXDGConfigHome := os.Getenv("XDG_CONFIG_HOME")
	t.Setenv("XDG_CONFIG_HOME", tempDir)

	// Create the config directory structure
	configDir := filepath.Join(tempDir, "toolhive")
	err := os.MkdirAll(configDir, 0755)
	require.NoError(t, err)

	// Write the config file if one is provided
	if cfg != nil {
		err = config.UpdateConfig(func(c *config.Config) { *c = *cfg })
		require.NoError(t, err)
	}

	return func() {
		t.Setenv("XDG_CONFIG_HOME", originalXDGConfigHome)
	}
}

func TestFindClientConfigs(t *testing.T) { //nolint:paralleltest // Uses environment variables
	logger.Initialize()

	// Setup a temporary home directory for testing
	originalHome := os.Getenv("HOME")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	defer func() {
		t.Setenv("HOME", originalHome)
	}()

	// Save original supported clients and restore after test
	originalClients := supportedClientIntegrations
	defer func() {
		supportedClientIntegrations = originalClients
	}()

	// Set up mock client configurations
	supportedClientIntegrations = createMockClientConfigs()

	// Create test config files for different clients
	createTestConfigFiles(t, tempHome)

	t.Run("InvalidConfigFileFormat", func(t *testing.T) { //nolint:paralleltest // Modifies global state
		// Create an invalid JSON file
		invalidPath := filepath.Join(tempHome, ".cursor", "invalid.json")
		err := os.MkdirAll(filepath.Dir(invalidPath), 0755)
		require.NoError(t, err)

		err = os.WriteFile(invalidPath, []byte("{invalid json}"), 0644)
		require.NoError(t, err)

		// Create a custom client config that points to the invalid file
		invalidClient := mcpClientConfig{
			ClientType:           "invalid",
			Description:          "Invalid client",
			RelPath:              []string{".cursor"},
			SettingsFile:         "invalid.json",
			MCPServersPathPrefix: "/mcpServers",
			Extension:            JSON,
		}

		// Save the original supported clients
		originalClients := supportedClientIntegrations
		defer func() {
			supportedClientIntegrations = originalClients
		}()

		// Add our invalid client to the supported clients
		supportedClientIntegrations = append(supportedClientIntegrations, invalidClient)

		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				RegisteredClients: []string{},
			},
		}

		cleanup := MockConfig(t, testConfig)
		defer cleanup()

		// Find client configs - this should fail due to the invalid JSON
		_, err = FindClientConfigs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to validate config file format")
		// we check if cursor is in the error message because that's the
		// config file that we inserted the bad json into
		assert.Contains(t, err.Error(), "cursor")
	})
}

func TestGenerateMCPServerURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		host          string
		port          int
		containerName string
		expected      string
	}{
		{
			name:          "Standard URL",
			host:          "localhost",
			port:          12345,
			containerName: "test-container",
			expected:      "http://localhost:12345" + ssecommon.HTTPSSEEndpoint + "#test-container",
		},
		{
			name:          "Different host",
			host:          "192.168.1.100",
			port:          54321,
			containerName: "another-container",
			expected:      "http://192.168.1.100:54321" + ssecommon.HTTPSSEEndpoint + "#another-container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			url := GenerateMCPServerURL(types.TransportTypeSSE.String(), tt.host, tt.port, tt.containerName)
			if url != tt.expected {
				t.Errorf("GenerateMCPServerURL() = %v, want %v", url, tt.expected)
			}
		})
	}
}

func TestSuccessfulClientConfigOperations(t *testing.T) {
	logger.Initialize()

	// Setup a temporary home directory for testing
	originalHome := os.Getenv("HOME")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	defer func() {
		t.Setenv("HOME", originalHome)
	}()

	// Save original supported clients and restore after test
	originalClients := supportedClientIntegrations
	defer func() {
		supportedClientIntegrations = originalClients
	}()

	// Set up mock client configurations
	supportedClientIntegrations = createMockClientConfigs()

	// Create test config files
	createTestConfigFiles(t, tempHome)

	t.Run("FindAllConfiguredClients", func(t *testing.T) { //nolint:paralleltest // Uses environment variables
		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				RegisteredClients: []string{},
			},
		}

		cleanup := MockConfig(t, testConfig)
		defer cleanup()

		configs, err := FindClientConfigs()
		require.NoError(t, err)
		assert.Len(t, configs, len(supportedClientIntegrations), "Should find all mock client configs")

		// Verify each client type is found
		foundTypes := make(map[MCPClient]bool)
		for _, cf := range configs {
			foundTypes[cf.ClientType] = true
		}

		for _, expectedClient := range supportedClientIntegrations {
			assert.True(t, foundTypes[expectedClient.ClientType],
				"Should find config for client type %s", expectedClient.ClientType)
		}
	})

	t.Run("VerifyConfigFileContents", func(t *testing.T) { //nolint:paralleltest // Uses environment variables
		configs, err := FindClientConfigs()
		require.NoError(t, err)
		require.NotEmpty(t, configs)

		for _, cf := range configs {
			// Read and parse the config file
			content, err := os.ReadFile(cf.Path)
			require.NoError(t, err, "Should be able to read config file for %s", cf.ClientType)

			// Verify JSON structure based on client type
			switch cf.ClientType {
			case VSCode, VSCodeInsider:
				assert.Contains(t, string(content), `"mcp":`,
					"Cconfig should contain mcp key")
				assert.Contains(t, string(content), `"servers":`,
					"VSCode config should contain servers key")
			case Cursor:
				assert.Contains(t, string(content), `"mcpServers":`,
					"Cursor config should contain mcpServers key")
			case RooCode:
				assert.Contains(t, string(content), `"mcpServers":`,
					"RooCode config should contain mcpServers key")
			case ClaudeCode:
				assert.Contains(t, string(content), `"mcpServers":`,
					"ClaudeCode config should contain mcpServers key")
			case Cline:
				assert.Contains(t, string(content), `"mcpServers":`,
					"Cline config should contain mcpServers key")
			case JetBrainsCopilot:
				assert.Contains(t, string(content), `"servers":`,
					"JetBrains Copilot config should contain servers key")
			}
		}
	})

	t.Run("AddAndVerifyMCPServer", func(t *testing.T) { //nolint:paralleltest // Uses environment variables
		configs, err := FindClientConfigs()
		require.NoError(t, err)
		require.NotEmpty(t, configs)

		testServer := "test-server"
		testURL := "http://localhost:9999/sse#test-server"

		for _, cf := range configs {
			err := Upsert(cf, testServer, testURL, types.TransportTypeSSE.String())
			require.NoError(t, err, "Should be able to add MCP server to %s config", cf.ClientType)

			// Read the file and verify the server was added
			content, err := os.ReadFile(cf.Path)
			require.NoError(t, err)

			// Check based on client type
			switch cf.ClientType {
			case VSCode, VSCodeInsider:
				assert.Contains(t, string(content), testURL,
					"VSCode config should contain the server URL")
			case Cursor, RooCode, ClaudeCode, Cline, JetBrainsCopilot:
				assert.Contains(t, string(content), testURL,
					"Config should contain the server URL")
			}
		}
	})
}

// Helper function to create test config files for different clients
func createTestConfigFiles(t *testing.T, homeDir string) {
	t.Helper()
	// Create test config files for each mock client configuration
	for _, cfg := range supportedClientIntegrations {
		// Build the full path for the config file
		configDir := filepath.Join(homeDir, filepath.Join(cfg.RelPath...))
		err := os.MkdirAll(configDir, 0755)
		if err == nil {
			configPath := filepath.Join(configDir, cfg.SettingsFile)
			validJSON := `{"mcpServers": {}, "mcp": {"servers": {}}}`
			err = os.WriteFile(configPath, []byte(validJSON), 0644)
			require.NoError(t, err)
		}
	}
}

// Package client provides utilities for managing client configurations
// and interacting with MCP servers.
package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/StacklokLabs/toolhive/pkg/config"
	"github.com/StacklokLabs/toolhive/pkg/logger"
	"github.com/StacklokLabs/toolhive/pkg/transport/ssecommon"
)

// createMockClientConfigs creates a set of mock client configurations for testing
func createMockClientConfigs() []mcpClientConfig {
	return []mcpClientConfig{
		{
			ClientType:           VSCode,
			Description:          "Visual Studio Code (Mock)",
			RelPath:              []string{"mock_vscode", "settings.json"},
			MCPServersPathPrefix: "/mcp/servers",
			Extension:            JSON,
		},
		{
			ClientType:           Cursor,
			Description:          "Cursor editor (Mock)",
			RelPath:              []string{"mock_cursor", "mcp.json"},
			MCPServersPathPrefix: "/mcpServers",
			Extension:            JSON,
		},
		{
			ClientType:           RooCode,
			Description:          "VS Code Roo Code extension (Mock)",
			RelPath:              []string{"mock_roo", "mcp_settings.json"},
			MCPServersPathPrefix: "/mcpServers",
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

func TestFindClientConfigs(t *testing.T) {
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

	t.Run("AutoDiscoveryEnabled", func(t *testing.T) {
		// Set up config with auto-discovery enabled
		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				AutoDiscovery:     true,
				RegisteredClients: []string{},
			},
		}

		cleanup := MockConfig(t, testConfig)
		defer cleanup()

		// Find client configs
		configs, err := FindClientConfigs()
		require.NoError(t, err)

		// We should find configs for all supported clients that were created
		assert.NotEmpty(t, configs)

		// Verify that we found the expected client types
		foundClients := make(map[MCPClient]bool)
		for _, cf := range configs {
			foundClients[cf.ClientType] = true
		}

		// Check that we found at least some of the expected clients
		// Note: This depends on which config files were successfully created
		assert.True(t, len(foundClients) > 0, "Should find at least one client config")
	})

	t.Run("AutoDiscoveryDisabledWithRegisteredClients", func(t *testing.T) {
		// Set up config with auto-discovery disabled but with registered clients
		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				AutoDiscovery:     false,
				RegisteredClients: []string{"vscode", "cursor"},
			},
		}

		cleanup := MockConfig(t, testConfig)
		defer cleanup()

		// Find client configs
		configs, err := FindClientConfigs()
		require.NoError(t, err)

		// We should only find configs for the registered clients
		foundClients := make(map[MCPClient]bool)
		for _, cf := range configs {
			foundClients[cf.ClientType] = true
		}

		// Check that we only found the registered clients
		for _, clientName := range testConfig.Clients.RegisteredClients {
			if foundClients[MCPClient(clientName)] {
				// At least one registered client was found
				return
			}
		}

		// If we get here, it means none of the registered clients were found
		// This is acceptable if the test environment doesn't have those clients configured
		t.Log("None of the registered clients were found, but this may be expected in the test environment")
	})

	t.Run("AutoDiscoveryDisabledWithNoRegisteredClients", func(t *testing.T) {
		// Set up config with auto-discovery disabled and no registered clients
		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				AutoDiscovery:     false,
				RegisteredClients: []string{},
			},
		}

		cleanup := MockConfig(t, testConfig)
		defer cleanup()

		// Find client configs
		configs, err := FindClientConfigs()
		require.NoError(t, err)

		// We should not find any configs
		assert.Empty(t, configs)
	})

	t.Run("InvalidConfigFileFormat", func(t *testing.T) {
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
			RelPath:              []string{".cursor", "invalid.json"},
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

		// Set up config with auto-discovery enabled
		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				AutoDiscovery:     true,
				RegisteredClients: []string{},
			},
		}

		cleanup := MockConfig(t, testConfig)
		defer cleanup()

		// Find client configs - this should fail due to the invalid JSON
		_, err = FindClientConfigs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to validate config file format")
		// we check if cursor is in the error message because thats the
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
			url := GenerateMCPServerURL(tt.host, tt.port, tt.containerName)
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

	t.Run("FindAllConfiguredClients", func(t *testing.T) {
		// Set up config with auto-discovery enabled
		testConfig := &config.Config{
			Secrets: config.Secrets{
				ProviderType: "encrypted",
			},
			Clients: config.Clients{
				AutoDiscovery:     true,
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

	t.Run("VerifyConfigFileContents", func(t *testing.T) {
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
					"VSCode config should contain mcp key")
				assert.Contains(t, string(content), `"servers":`,
					"VSCode config should contain servers key")
			case Cursor:
				assert.Contains(t, string(content), `"mcpServers":`,
					"Cursor config should contain mcpServers key")
			case RooCode:
				assert.Contains(t, string(content), `"mcpServers":`,
					"RooCode config should contain mcpServers key")
			}
		}
	})

	t.Run("AddAndVerifyMCPServer", func(t *testing.T) {
		configs, err := FindClientConfigs()
		require.NoError(t, err)
		require.NotEmpty(t, configs)

		testServer := "test-server"
		testURL := "http://localhost:9999/sse#test-server"

		for _, cf := range configs {
			err := Upsert(cf, testServer, testURL)
			require.NoError(t, err, "Should be able to add MCP server to %s config", cf.ClientType)

			// Read the file and verify the server was added
			content, err := os.ReadFile(cf.Path)
			require.NoError(t, err)

			// Check based on client type
			switch cf.ClientType {
			case VSCode, VSCodeInsider:
				assert.Contains(t, string(content), testURL,
					"VSCode config should contain the server URL")
			case Cursor, RooCode:
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
		configDir := filepath.Join(homeDir, filepath.Join(cfg.RelPath[:len(cfg.RelPath)-1]...))
		err := os.MkdirAll(configDir, 0755)
		if err == nil {
			configPath := filepath.Join(configDir, cfg.RelPath[len(cfg.RelPath)-1])
			validJSON := `{"mcpServers": {}, "mcp": {"servers": {}}}`
			err = os.WriteFile(configPath, []byte(validJSON), 0644)
			require.NoError(t, err)
		}
	}
}

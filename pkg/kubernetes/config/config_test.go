package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// SetupTestConfig creates a temporary config file and returns the config path
func SetupTestConfig(t *testing.T, configContent *Config) (string, string) {
	t.Helper()
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create config directory
	configDir := filepath.Join(tempDir, "toolhive")
	err := os.MkdirAll(configDir, 0755)
	require.NoError(t, err)

	// Set up the config file path
	configPath := filepath.Join(configDir, "config.yaml")

	// If config content is provided, write it to the file
	if configContent != nil {
		configBytes, err := yaml.Marshal(configContent)
		require.NoError(t, err)

		err = os.WriteFile(configPath, configBytes, 0600)
		require.NoError(t, err)
	}

	return tempDir, configPath
}


func TestRegistryURLConfig(t *testing.T) {
	t.Parallel()
	logger.Initialize()

	t.Run("TestSetAndGetRegistryURL", func(t *testing.T) {
		t.Parallel()
		tempDir, configPath := SetupTestConfig(t, &Config{
			RegistryUrl: "",
		})

		// Test setting a registry URL
		testURL := "https://example.com/registry.json"
		err := UpdateConfigAtPath(configPath, func(c *Config) {
			c.RegistryUrl = testURL
		})
		require.NoError(t, err)

		// Load the config and verify the URL was set
		config, err := LoadOrCreateConfigWithPath(configPath)
		require.NoError(t, err)
		assert.Equal(t, testURL, config.RegistryUrl)

		// Test unsetting the registry URL
		err = UpdateConfigAtPath(configPath, func(c *Config) {
			c.RegistryUrl = ""
		})
		require.NoError(t, err)

		// Load the config and verify the URL was unset
		config, err = LoadOrCreateConfigWithPath(configPath)
		require.NoError(t, err)
		assert.Equal(t, "", config.RegistryUrl)

		t.Cleanup(func() {
			if err := os.RemoveAll(tempDir); err != nil {
				t.Logf("Failed to remove temp dir: %v", err)
			}
		})
	})

	t.Run("TestRegistryURLPersistence", func(t *testing.T) {
		t.Parallel()
		tempDir, configPath := SetupTestConfig(t, nil)

		testURL := "https://custom-registry.example.com/registry.json"

		// Set the registry URL
		err := UpdateConfigAtPath(configPath, func(c *Config) {
			c.RegistryUrl = testURL
		})
		require.NoError(t, err)

		// Load config again to verify persistence
		config, err := LoadOrCreateConfigWithPath(configPath)
		require.NoError(t, err)
		assert.Equal(t, testURL, config.RegistryUrl)

		t.Cleanup(func() {
			if err := os.RemoveAll(tempDir); err != nil {
				t.Logf("Failed to remove temp dir: %v", err)
			}
		})
	})

	t.Run("TestAllowPrivateRegistryIp", func(t *testing.T) {
		t.Parallel()
		tempDir, configPath := SetupTestConfig(t, &Config{
			RegistryUrl:            "",
			AllowPrivateRegistryIp: false,
		})

		// Test enabling
		err := UpdateConfigAtPath(configPath, func(c *Config) {
			c.AllowPrivateRegistryIp = true
		})
		require.NoError(t, err)

		// Load the config and verify the setting was toggled to true
		config, err := LoadOrCreateConfigWithPath(configPath)
		require.NoError(t, err)
		assert.Equal(t, true, config.AllowPrivateRegistryIp)

		// Test toggling setting to false
		err = UpdateConfigAtPath(configPath, func(c *Config) {
			c.AllowPrivateRegistryIp = false
		})
		require.NoError(t, err)

		// Load the config and verify the setting was toggled to false
		config, err = LoadOrCreateConfigWithPath(configPath)
		require.NoError(t, err)
		assert.Equal(t, false, config.AllowPrivateRegistryIp)

		t.Cleanup(func() {
			if err := os.RemoveAll(tempDir); err != nil {
				t.Logf("Failed to remove temp dir: %v", err)
			}
		})
	})
}

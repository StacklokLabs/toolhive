// Package config contains the definition of the application config structure
// and logic required to load and update it.
package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/adrg/xdg"
	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"

	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/secrets"
)

// lockTimeout is the maximum time to wait for a file lock
const lockTimeout = 1 * time.Second

// Config represents the configuration of the application.
type Config struct {
	Secrets           Secrets `yaml:"secrets"`
	Clients           Clients `yaml:"clients"`
	RegistryUrl       string  `yaml:"registry_url"`
	CACertificatePath string  `yaml:"ca_certificate_path,omitempty"`
}

// Secrets contains the settings for secrets management.
type Secrets struct {
	ProviderType string `yaml:"provider_type"`
}

// GetProviderType returns the secrets provider type from the application config.
func (s *Secrets) GetProviderType() (secrets.ProviderType, error) {
	provider := s.ProviderType
	switch provider {
	case string(secrets.EncryptedType):
		return secrets.EncryptedType, nil
	case string(secrets.OnePasswordType):
		return secrets.OnePasswordType, nil
	default:
		// TODO: auto-generate the set of valid values.
		return "", fmt.Errorf("invalid secrets provider type: %s (valid types: encrypted, 1password)", provider)
	}
}

// Clients contains settings for client configuration.
type Clients struct {
	AutoDiscovery     bool     `yaml:"auto_discovery"`
	RegisteredClients []string `yaml:"registered_clients"`
}

// defaultPathGenerator generates the default config path using xdg
var defaultPathGenerator = func() (string, error) {
	return xdg.ConfigFile("toolhive/config.yaml")
}

// getConfigPath is the current path generator, can be replaced in tests
var getConfigPath = defaultPathGenerator

// LoadOrCreateConfig fetches the application configuration.
// If it does not already exist - it will create a new config file with default values.
func LoadOrCreateConfig() (*Config, error) {
	var config Config

	configPath, err := getConfigPath()
	if err != nil {
		return nil, fmt.Errorf("unable to fetch config path: %w", err)
	}

	// Check to see if the config file already exists.
	configPath = path.Clean(configPath)
	newConfig := false
	// #nosec G304: File path is not configurable at this time.
	_, err = os.Stat(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			newConfig = true
		} else {
			return nil, fmt.Errorf("failed to stat secrets file: %w", err)
		}
	}

	if newConfig {
		// Create a new config with default values.
		config = Config{
			Secrets: Secrets{
				ProviderType: string(secrets.EncryptedType),
			},
			Clients: Clients{
				AutoDiscovery: true,
			},
			RegistryUrl: "",
		}

		// Prompt user explicitly for auto discovery behaviour.
		logger.Info("Would you like to enable auto discovery and configuration of MCP clients? (y/n) [n]: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			logger.Info("Unable to read input, defaulting to No.")
		}
		// Treat anything except y/Y as n.
		if input == "y\n" || input == "Y\n" {
			config.Clients.AutoDiscovery = true
		} else {
			config.Clients.AutoDiscovery = false
		}

		// Persist the new default to disk.
		logger.Infof("initializing configuration file at %s", configPath)
		err = config.save()
		if err != nil {
			return nil, fmt.Errorf("failed to write default config: %w", err)
		}
	} else {
		// Load the existing config and decode.
		// #nosec G304: File path is not configurable at this time.
		configFile, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read config file %s: %w", configPath, err)
		}
		err = yaml.Unmarshal(configFile, &config)
		if err != nil {
			return nil, fmt.Errorf("failed to parse config file yaml: %w", err)
		}
		// Hack - if the secrets provider type is set to the old `basic` type,
		// just change it to `encrypted`.
		if config.Secrets.ProviderType == "basic" {
			fmt.Println("cleaning up basic secrets provider")
			// Attempt to cleanup path, treat errors as non fatal.
			oldPath, err := xdg.DataFile("toolhive/secrets")
			if err == nil {
				_ = os.Remove(oldPath)
			}
			config.Secrets.ProviderType = string(secrets.EncryptedType)
			err = config.save()
			if err != nil {
				fmt.Printf("error updating config: %v", err)
			}
		}
	}

	return &config, nil
}

// Save serializes the config struct and writes it to disk.
func (c *Config) save() error {
	configPath, err := getConfigPath()
	if err != nil {
		return fmt.Errorf("unable to fetch config path: %w", err)
	}

	configBytes, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("error serializing config file: %w", err)
	}

	err = os.WriteFile(configPath, configBytes, 0600)
	if err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}
	return nil
}

// UpdateConfig locks a separate lock file, reads from disk, applies the changes
// from the anonymous function, writes to disk and unlocks the file.
func UpdateConfig(updateFn func(*Config)) error {
	configPath, err := getConfigPath()
	if err != nil {
		return fmt.Errorf("unable to fetch config path: %w", err)
	}

	// Use a separate lock file for cross-platform compatibility
	lockPath := configPath + ".lock"
	fileLock := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	// Try and acquire a file lock.
	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("failed to acquire lock: timeout after %v", lockTimeout)
	}
	defer fileLock.Unlock()

	// Load the config after acquiring the lock to avoid race conditions
	c, err := LoadOrCreateConfig()
	if err != nil {
		return fmt.Errorf("failed to load config from disk: %w", err)
	}

	// Apply changes to the config file.
	updateFn(c)

	// Write the updated config to disk.
	err = c.save()
	if err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Lock is released automatically when the function returns.
	return nil
}

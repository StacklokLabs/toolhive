// Package config contains the definition of the application config structure
// and logic required to load and update it.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/adrg/xdg"
	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/secrets"
)

// lockTimeout is the maximum time to wait for a file lock
const lockTimeout = 1 * time.Second

// Config represents the configuration of the application.
type Config struct {
	Secrets                Secrets             `yaml:"secrets"`
	Clients                Clients             `yaml:"clients"`
	RegistryUrl            string              `yaml:"registry_url"`
	AllowPrivateRegistryIp bool                `yaml:"allow_private_registry_ip"`
	CACertificatePath      string              `yaml:"ca_certificate_path,omitempty"`
	OTEL                   OpenTelemetryConfig `yaml:"otel,omitempty"`
}

// Secrets contains the settings for secrets management.
type Secrets struct {
	ProviderType   string `yaml:"provider_type"`
	SetupCompleted bool   `yaml:"setup_completed"`
}

// validateProviderType validates and returns the secrets provider type.
func validateProviderType(provider string) (secrets.ProviderType, error) {
	switch provider {
	case string(secrets.EncryptedType):
		return secrets.EncryptedType, nil
	case string(secrets.OnePasswordType):
		return secrets.OnePasswordType, nil
	case string(secrets.NoneType):
		return secrets.NoneType, nil
	default:
		return "", fmt.Errorf("invalid secrets provider type: %s (valid types: %s, %s, %s)",
			provider, string(secrets.EncryptedType), string(secrets.OnePasswordType), string(secrets.NoneType))
	}
}

// GetProviderType returns the secrets provider type from the environment variable or application config.
// It first checks the TOOLHIVE_SECRETS_PROVIDER environment variable, and falls back to the config file.
// Returns ErrSecretsNotSetup if secrets have not been configured yet.
func (s *Secrets) GetProviderType() (secrets.ProviderType, error) {
	// Check if secrets setup has been completed
	if !s.SetupCompleted {
		return "", secrets.ErrSecretsNotSetup
	}

	// First check the environment variable
	if envProvider := os.Getenv(secrets.ProviderEnvVar); envProvider != "" {
		return validateProviderType(envProvider)
	}

	// Fall back to config file
	return validateProviderType(s.ProviderType)
}

// Clients contains settings for client configuration.
type Clients struct {
	RegisteredClients []string `yaml:"registered_clients"`
	AutoDiscovery     bool     `yaml:"auto_discovery"` // Deprecated: kept for migration purposes only
}

// defaultPathGenerator generates the default config path using xdg
var defaultPathGenerator = func() (string, error) {
	return xdg.ConfigFile("toolhive/config.yaml")
}

// getConfigPath is the current path generator, can be replaced in tests
var getConfigPath = defaultPathGenerator

// createNewConfigWithDefaults creates a new config with default values
func createNewConfigWithDefaults() Config {
	return Config{
		Secrets: Secrets{
			ProviderType:   "", // No default provider - user must run setup
			SetupCompleted: false,
		},
		RegistryUrl:            "",
		AllowPrivateRegistryIp: false,
	}
}

// applyBackwardCompatibility applies backward compatibility fixes to existing configs
func applyBackwardCompatibility(config *Config) error {
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
			return fmt.Errorf("error updating config: %v", err)
		}
	}

	// Handle backward compatibility: if provider is set but setup_completed is false,
	// consider it as setup completed (for existing users)
	if config.Secrets.ProviderType != "" && !config.Secrets.SetupCompleted {
		config.Secrets.SetupCompleted = true
		err := config.save()
		if err != nil {
			return fmt.Errorf("error updating config for backward compatibility: %v", err)
		}
	}

	return nil
}

// LoadOrCreateConfig fetches the application configuration.
// If it does not already exist - it will create a new config file with default values.
func LoadOrCreateConfig() (*Config, error) {
	return LoadOrCreateConfigWithPath("")
}

// LoadOrCreateConfigWithPath fetches the application configuration from a specific path.
// If configPath is empty, it uses the default path.
// If it does not already exist - it will create a new config file with default values.
func LoadOrCreateConfigWithPath(configPath string) (*Config, error) {
	var config Config
	var err error

	if configPath == "" {
		configPath, err = getConfigPath()
		if err != nil {
			return nil, fmt.Errorf("unable to fetch config path: %w", err)
		}
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
		config = createNewConfigWithDefaults()

		// Persist the new default to disk.
		logger.Debugf("initializing configuration file at %s", configPath)
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

		// Apply backward compatibility fixes
		err = applyBackwardCompatibility(&config)
		if err != nil {
			return nil, fmt.Errorf("failed to apply backward compatibility fixes: %w", err)
		}
	}

	return &config, nil
}

// Save serializes the config struct and writes it to disk.
func (c *Config) save() error {
	return c.saveToPath("")
}

// saveToPath serializes the config struct and writes it to a specific path.
// If configPath is empty, it uses the default path.
func (c *Config) saveToPath(configPath string) error {
	if configPath == "" {
		var err error
		configPath, err = getConfigPath()
		if err != nil {
			return fmt.Errorf("unable to fetch config path: %w", err)
		}
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
	return UpdateConfigAtPath("", updateFn)
}

// UpdateConfigAtPath locks a separate lock file, reads from disk, applies the changes
// from the anonymous function, writes to disk and unlocks the file.
// If configPath is empty, it uses the default path.
func UpdateConfigAtPath(configPath string, updateFn func(*Config)) error {
	if configPath == "" {
		var err error
		configPath, err = getConfigPath()
		if err != nil {
			return fmt.Errorf("unable to fetch config path: %w", err)
		}
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
	c, err := LoadOrCreateConfigWithPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config from disk: %w", err)
	}

	// Apply changes to the config file.
	updateFn(c)

	// Write the updated config to disk.
	err = c.saveToPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Lock is released automatically when the function returns.
	return nil
}

// OpenTelemetryConfig contains the settings for OpenTelemetry configuration.
type OpenTelemetryConfig struct {
	Endpoint     string   `yaml:"endpoint,omitempty"`
	SamplingRate float64  `yaml:"sampling-rate,omitempty"`
	EnvVars      []string `yaml:"env-vars,omitempty"`
}

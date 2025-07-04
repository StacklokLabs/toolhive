// Package authz provides authorization utilities using Cedar policies.
package authz

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// ConfigType represents the type of authorization configuration.
type ConfigType string

const (
	// ConfigTypeCedarV1 represents the Cedar v1 authorization configuration.
	ConfigTypeCedarV1 ConfigType = "cedarv1"
)

// Config represents the authorization configuration.
type Config struct {
	// Version is the version of the configuration format.
	Version string `json:"version" yaml:"version"`

	// Type is the type of authorization configuration.
	Type ConfigType `json:"type" yaml:"type"`

	// Cedar is the Cedar-specific configuration.
	// This is only used when Type is ConfigTypeCedarV1.
	Cedar *CedarConfig `json:"cedar,omitempty" yaml:"cedar,omitempty"`
}

// CedarConfig represents the Cedar-specific authorization configuration.
type CedarConfig struct {
	// Policies is a list of Cedar policy strings
	Policies []string `json:"policies" yaml:"policies"`

	// EntitiesJSON is the JSON string representing Cedar entities
	EntitiesJSON string `json:"entities_json" yaml:"entities_json"`
}

// LoadConfig loads the authorization configuration from a file.
// It supports both JSON and YAML formats, detected by file extension.
//
//nolint:gosec // This is intentionally loading a file specified by the user
func LoadConfig(path string) (*Config, error) {
	// Read the file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read authorization configuration file: %w", err)
	}

	// Determine the file format based on extension
	var config Config
	ext := strings.ToLower(filepath.Ext(path))

	// Parse the file based on its format
	switch ext {
	case ".yaml", ".yml":
		// Parse YAML
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse YAML authorization configuration file: %w", err)
		}
	case ".json", "":
		// Parse JSON (default if no extension)
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse JSON authorization configuration file: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported file format: %s (supported formats: .json, .yaml, .yml)", ext)
	}

	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// Validate validates the authorization configuration.
func (c *Config) Validate() error {
	// Check if the version is provided
	if c.Version == "" {
		return fmt.Errorf("version is required")
	}

	// Check if the type is provided
	if c.Type == "" {
		return fmt.Errorf("type is required")
	}

	// Validate based on the type
	switch c.Type {
	case ConfigTypeCedarV1:
		// Check if the Cedar configuration is provided
		if c.Cedar == nil {
			return fmt.Errorf("cedar configuration is required for type %s", c.Type)
		}

		// Check if policies are provided
		if len(c.Cedar.Policies) == 0 {
			return fmt.Errorf("at least one policy is required for type %s", c.Type)
		}
	default:
		return fmt.Errorf("unsupported configuration type: %s", c.Type)
	}

	return nil
}

// CreateMiddleware creates an HTTP middleware from the configuration.
func (c *Config) CreateMiddleware() (func(http.Handler) http.Handler, error) {
	// Create the appropriate middleware based on the configuration type
	switch c.Type {
	case ConfigTypeCedarV1:
		// Create the Cedar authorizer
		authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
			Policies:     c.Cedar.Policies,
			EntitiesJSON: c.Cedar.EntitiesJSON,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Cedar authorizer: %w", err)
		}

		// Return the Cedar middleware
		return authorizer.Middleware, nil
	default:
		return nil, fmt.Errorf("unsupported configuration type: %s", c.Type)
	}
}

// GetMiddlewareFromFile loads the authorization configuration from a file and creates an HTTP middleware.
func GetMiddlewareFromFile(path string) (func(http.Handler) http.Handler, error) {
	// Load the configuration
	config, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	// Create the middleware
	return config.CreateMiddleware()
}

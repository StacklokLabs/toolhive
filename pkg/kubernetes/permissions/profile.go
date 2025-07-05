// Package permissions provides utilities for managing container permissions
// and permission profiles for the toolhive application.
package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Built-in permission profile names
const (
	// ProfileNone is the name of the built-in profile with no permissions
	ProfileNone = "none"
	// ProfileNetwork is the name of the built-in profile with network permissions
	ProfileNetwork = "network"
)

// Profile represents a permission profile for a container
type Profile struct {
	// Name is the name of the profile
	Name string `json:"name,omitempty"`

	// Read is a list of mount declarations that the container can read from
	// These can be in the following formats:
	// - A single path: The same path will be mounted from host to container
	// - host-path:container-path: Different paths for host and container
	// - resource-uri:container-path: Mount a resource identified by URI to a container path
	Read []MountDeclaration `json:"read,omitempty"`

	// Write is a list of mount declarations that the container can write to
	// These follow the same format as Read mounts but with write permissions
	Write []MountDeclaration `json:"write,omitempty"`

	// Network defines network permissions
	Network *NetworkPermissions `json:"network,omitempty"`
}

// NetworkPermissions defines network permissions for a container
type NetworkPermissions struct {
	// Outbound defines outbound network permissions
	Outbound *OutboundNetworkPermissions `json:"outbound,omitempty"`
}

// OutboundNetworkPermissions defines outbound network permissions
type OutboundNetworkPermissions struct {
	// InsecureAllowAll allows all outbound network connections
	InsecureAllowAll bool `json:"insecure_allow_all,omitempty"`

	// AllowTransport is a list of allowed transport protocols (tcp, udp)
	AllowTransport []string `json:"allow_transport,omitempty"`

	// AllowHost is a list of allowed hosts
	AllowHost []string `json:"allow_host,omitempty"`

	// AllowPort is a list of allowed ports
	AllowPort []int `json:"allow_port,omitempty"`
}

// NewProfile creates a new permission profile
func NewProfile() *Profile {
	return &Profile{
		Name:  ProfileNone,
		Read:  []MountDeclaration{},
		Write: []MountDeclaration{},
		Network: &NetworkPermissions{
			Outbound: &OutboundNetworkPermissions{
				InsecureAllowAll: false,
				AllowTransport:   []string{},
				AllowHost:        []string{},
				AllowPort:        []int{},
			},
		},
	}
}

// FromFile loads a permission profile from a file
func FromFile(path string) (*Profile, error) {
	// Read the file
	// #nosec G304 - This is intentional as we're reading a user-specified permission profile
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read permission profile: %w", err)
	}

	// Parse the JSON
	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("failed to parse permission profile: %w", err)
	}

	return &profile, nil
}

// BuiltinNoneProfile returns the built-in profile with no permissions
func BuiltinNoneProfile() *Profile {
	return &Profile{
		Name:  ProfileNone,
		Read:  []MountDeclaration{},
		Write: []MountDeclaration{},
		Network: &NetworkPermissions{
			Outbound: &OutboundNetworkPermissions{
				InsecureAllowAll: false,
				AllowTransport:   []string{},
				AllowHost:        []string{},
				AllowPort:        []int{},
			},
		},
	}
}

// BuiltinNetworkProfile returns the built-in network profile
func BuiltinNetworkProfile() *Profile {
	return &Profile{
		Name:  ProfileNetwork,
		Read:  []MountDeclaration{},
		Write: []MountDeclaration{},
		Network: &NetworkPermissions{
			Outbound: &OutboundNetworkPermissions{
				InsecureAllowAll: true,
				AllowTransport:   []string{},
				AllowHost:        []string{},
				AllowPort:        []int{},
			},
		},
	}
}

// MountDeclaration represents a mount declaration for a container
// It can be in one of the following formats:
//   - A single path: The same path will be mounted from host to container
//   - host-path:container-path: Different paths for host and container
//   - resource-uri:container-path: Mount a resource identified by URI to a container path
//     (e.g., volume://name:container-path)
type MountDeclaration string

// Regular expressions for parsing mount declarations
var (
	// resourceURIRegex matches resource URIs like "volume://name:container-path"
	// The scheme must start with a letter and can contain letters, numbers, underscores, and hyphens
	// The resource name can contain any characters except colon
	// The container path can contain any characters except colon
	resourceURIRegex = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_-]*)://([^:]+):([^:]+)$`)

	// hostPathRegex matches host-path:container-path format
	// Both host path and container path can contain any characters except colon
	hostPathRegex = regexp.MustCompile(`^([^:]+):([^:]+)$`)

	// commandInjectionPattern matches common command injection patterns
	commandInjectionPattern = regexp.MustCompile(`[$&;|]|\$\(|\` + "`")
)

// validatePath checks if a path contains potentially dangerous patterns
func validatePath(path string) error {
	if commandInjectionPattern.MatchString(path) {
		return fmt.Errorf("potential command injection detected in path: %s", path)
	}

	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("null byte detected in path: %s", path)
	}

	return nil
}

// cleanPath cleans a path using filepath.Clean
func cleanPath(path string) string {
	return filepath.Clean(path)
}

// Parse parses a mount declaration and returns the source and target paths
// It also cleans and validates the paths
func (m MountDeclaration) Parse() (source, target string, err error) {
	declaration := string(m)

	// Check if it's a resource URI
	if matches := resourceURIRegex.FindStringSubmatch(declaration); matches != nil {
		scheme := matches[1]
		resourceName := matches[2]
		containerPath := matches[3]

		// Validate paths
		if err := validatePath(resourceName); err != nil {
			return "", "", err
		}
		if err := validatePath(containerPath); err != nil {
			return "", "", err
		}

		// Clean paths
		cleanedResource := cleanPath(resourceName)
		cleanedTarget := cleanPath(containerPath)

		return scheme + "://" + cleanedResource, cleanedTarget, nil
	}

	// Check if it's a host-path:container-path format
	if matches := hostPathRegex.FindStringSubmatch(declaration); matches != nil {
		hostPath := matches[1]
		containerPath := matches[2]

		// Validate paths
		if err := validatePath(hostPath); err != nil {
			return "", "", err
		}
		if err := validatePath(containerPath); err != nil {
			return "", "", err
		}

		// Clean paths
		cleanedSource := cleanPath(hostPath)
		cleanedTarget := cleanPath(containerPath)

		return cleanedSource, cleanedTarget, nil
	}

	// If it doesn't contain a colon, it's a single path
	if !strings.Contains(declaration, ":") {
		// Validate path
		if err := validatePath(declaration); err != nil {
			return "", "", err
		}

		// Clean path
		cleanedPath := cleanPath(declaration)

		return cleanedPath, cleanedPath, nil
	}

	// If we get here, the format is invalid
	return "", "", fmt.Errorf("invalid mount declaration format: %s "+
		"(expected path, host-path:container-path, or scheme://resource:container-path)", declaration)
}

// IsValid checks if the mount declaration is valid
func (m MountDeclaration) IsValid() bool {
	_, _, err := m.Parse()
	return err == nil
}

// IsResourceURI checks if the mount declaration is a resource URI
func (m MountDeclaration) IsResourceURI() bool {
	return resourceURIRegex.MatchString(string(m))
}

// GetResourceType returns the resource type if the mount declaration is a resource URI
// For example, "volume://name" would return "volume"
func (m MountDeclaration) GetResourceType() (string, error) {
	matches := resourceURIRegex.FindStringSubmatch(string(m))
	if matches == nil {
		return "", fmt.Errorf("not a resource URI: %s", m)
	}

	return matches[1], nil
}

// ParseMountDeclarations parses a list of mount declarations
func ParseMountDeclarations(declarations []string) ([]MountDeclaration, error) {
	result := make([]MountDeclaration, 0, len(declarations))

	for _, declaration := range declarations {
		mount := MountDeclaration(declaration)

		// Check if the declaration is valid
		if !mount.IsValid() {
			_, _, err := mount.Parse()
			return nil, fmt.Errorf("invalid mount declaration: %s (%v)", declaration, err)
		}

		result = append(result, mount)
	}

	return result, nil
}

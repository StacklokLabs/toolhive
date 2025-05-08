// Package registry provides access to the MCP server registry
package registry

import (
	"time"

	"github.com/stacklok/toolhive/pkg/permissions"
)

// Registry represents the top-level structure of the MCP registry
type Registry struct {
	// Version is the schema version of the registry
	Version string `json:"version"`
	// LastUpdated is the timestamp when the registry was last updated, in RFC3339 format
	LastUpdated string `json:"last_updated"`
	// Servers is a map of server names to their corresponding server definitions
	Servers map[string]*Server `json:"servers"`
}

// Server represents an MCP server in the registry
type Server struct {
	// Name is the identifier for the MCP server, used when referencing the server in commands
	// If not provided, it will be auto-generated from the image name
	Name string `json:"name,omitempty"`
	// Image is the Docker image reference for the MCP server
	Image string `json:"image"`
	// Description is a human-readable description of the server's purpose and functionality
	Description string `json:"description"`
	// Transport defines the communication protocol for the server (stdio or sse)
	Transport string `json:"transport"`
	// TargetPort is the port for the container to expose (only applicable to SSE transport)
	TargetPort int `json:"target_port,omitempty"`
	// Permissions defines the security profile and access permissions for the server
	Permissions *permissions.Profile `json:"permissions"`
	// Tools is a list of tool names provided by this MCP server
	Tools []string `json:"tools"`
	// EnvVars defines environment variables that can be passed to the server
	EnvVars []*EnvVar `json:"env_vars"`
	// Args are the default command-line arguments to pass to the MCP server container.
	// These arguments will be prepended to any command-line arguments provided by the user.
	Args []string `json:"args"`
	// Metadata contains additional information about the server such as popularity metrics
	Metadata *Metadata `json:"metadata"`
	// RepositoryURL is the URL to the source code repository for the server
	RepositoryURL string `json:"repository_url,omitempty"`
	// Tags are categorization labels for the server to aid in discovery and filtering
	Tags []string `json:"tags,omitempty"`
	// DockerTags lists the available Docker tags for this server image
	DockerTags []string `json:"docker_tags,omitempty"`
}

// EnvVar represents an environment variable for an MCP server
type EnvVar struct {
	// Name is the environment variable name (e.g., API_KEY)
	Name string `json:"name"`
	// Description is a human-readable explanation of the variable's purpose
	Description string `json:"description"`
	// Required indicates whether this environment variable must be provided
	// If true and not provided via command line or secrets, the user will be prompted for a value
	Required bool `json:"required"`
	// Default is the value to use if the environment variable is not explicitly provided
	// Only used for non-required variables
	Default string `json:"default,omitempty"`
}

// Metadata represents metadata about an MCP server
type Metadata struct {
	// Stars represents the popularity rating or number of stars for the server
	Stars int `json:"stars"`
	// Pulls indicates how many times the server image has been downloaded
	Pulls int `json:"pulls"`
	// LastUpdated is the timestamp when the server was last updated, in RFC3339 format
	LastUpdated string `json:"last_updated"`
}

// ParsedTime returns the LastUpdated field as a time.Time
func (m *Metadata) ParsedTime() (time.Time, error) {
	return time.Parse(time.RFC3339, m.LastUpdated)
}

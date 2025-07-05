// Package registry provides access to the MCP server registry
package registry

import (
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/permissions"
)

// Updates to the registry schema should be reflected in the JSON schema file located at docs/registry/schema.json.
// The schema is used for validation and documentation purposes.

// Registry represents the top-level structure of the MCP registry
type Registry struct {
	// Version is the schema version of the registry
	Version string `json:"version"`
	// LastUpdated is the timestamp when the registry was last updated, in RFC3339 format
	LastUpdated string `json:"last_updated"`
	// Servers is a map of server names to their corresponding server definitions
	Servers map[string]*ImageMetadata `json:"servers"`
}

// ImageMetadata represents the metadata for an MCP server image stored in our registry.
type ImageMetadata struct {
	// Name is the identifier for the MCP server, used when referencing the server in commands
	// If not provided, it will be auto-generated from the image name
	Name string `json:"name,omitempty"`
	// Image is the Docker image reference for the MCP server
	Image string `json:"image"`
	// Description is a human-readable description of the server's purpose and functionality
	Description string `json:"description"`
	// Tier represents the tier classification level of the server, e.g., "official" or "community" driven
	Tier string `json:"tier"`
	// The Status indicates whether the server is currently active or deprecated
	Status string `json:"status"`
	// Transport defines the communication protocol for the server (stdio, sse, or streamable-http)
	Transport string `json:"transport"`
	// TargetPort is the port for the container to expose (only applicable to SSE and Streamable HTTP transports)
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
	// Provenance contains verification and signing metadata
	Provenance *Provenance `json:"provenance,omitempty"`
}

// Provenance contains metadata about the image's provenance and signing status
type Provenance struct {
	SigstoreURL       string               `json:"sigstore_url"`
	RepositoryURI     string               `json:"repository_uri"`
	RepositoryRef     string               `json:"repository_ref"`
	SignerIdentity    string               `json:"signer_identity"`
	RunnerEnvironment string               `json:"runner_environment"`
	CertIssuer        string               `json:"cert_issuer"`
	Attestation       *VerifiedAttestation `json:"attestation,omitempty"`
}

// VerifiedAttestation represents the verified attestation information
type VerifiedAttestation struct {
	PredicateType string `json:"predicate_type,omitempty"`
	Predicate     any    `json:"predicate,omitempty"`
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
	// Secret indicates whether this environment variable contains sensitive information
	// If true, the value will be stored as a secret rather than as a plain environment variable
	Secret bool `json:"secret,omitempty"`
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

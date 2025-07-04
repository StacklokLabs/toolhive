package registry

// Provider defines the interface for registry storage implementations
type Provider interface {
	// GetRegistry returns the complete registry data
	GetRegistry() (*Registry, error)

	// GetServer returns a specific server by name
	GetServer(name string) (*ImageMetadata, error)

	// SearchServers searches for servers matching the query
	SearchServers(query string) ([]*ImageMetadata, error)

	// ListServers returns all available servers
	ListServers() ([]*ImageMetadata, error)
}

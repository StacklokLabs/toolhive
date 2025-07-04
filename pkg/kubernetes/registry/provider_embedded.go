package registry

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed data/registry.json
var embeddedRegistryFS embed.FS

// EmbeddedRegistryProvider provides registry data from embedded JSON files
type EmbeddedRegistryProvider struct {
	registry     *Registry
	registryOnce sync.Once
	registryErr  error
}

// NewEmbeddedRegistryProvider creates a new embedded registry provider
func NewEmbeddedRegistryProvider() *EmbeddedRegistryProvider {
	return &EmbeddedRegistryProvider{}
}

// GetRegistry returns the embedded registry data
func (p *EmbeddedRegistryProvider) GetRegistry() (*Registry, error) {
	p.registryOnce.Do(func() {
		data, err := embeddedRegistryFS.ReadFile("data/registry.json")
		if err != nil {
			p.registryErr = fmt.Errorf("failed to read embedded registry data: %w", err)
			return
		}

		p.registry, p.registryErr = parseRegistryData(data)
		if p.registryErr != nil {
			return
		}

		// Set name field on each server based on map key
		for name, server := range p.registry.Servers {
			server.Name = name
		}
	})

	return p.registry, p.registryErr
}

// GetServer returns a specific server by name
func (p *EmbeddedRegistryProvider) GetServer(name string) (*ImageMetadata, error) {
	reg, err := p.GetRegistry()
	if err != nil {
		return nil, err
	}

	server, ok := reg.Servers[name]
	if !ok {
		return nil, fmt.Errorf("server not found: %s", name)
	}

	return server, nil
}

// SearchServers searches for servers matching the query
func (p *EmbeddedRegistryProvider) SearchServers(query string) ([]*ImageMetadata, error) {
	reg, err := p.GetRegistry()
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []*ImageMetadata

	for name, server := range reg.Servers {
		// Search in name
		if strings.Contains(strings.ToLower(name), query) {
			results = append(results, server)
			continue
		}

		// Search in description
		if strings.Contains(strings.ToLower(server.Description), query) {
			results = append(results, server)
			continue
		}

		// Search in tags
		for _, tag := range server.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				results = append(results, server)
				break
			}
		}
	}

	return results, nil
}

// ListServers returns all available servers
func (p *EmbeddedRegistryProvider) ListServers() ([]*ImageMetadata, error) {
	reg, err := p.GetRegistry()
	if err != nil {
		return nil, err
	}

	servers := make([]*ImageMetadata, 0, len(reg.Servers))
	for _, server := range reg.Servers {
		servers = append(servers, server)
	}

	return servers, nil
}

// parseRegistryData parses JSON data into a Registry struct
func parseRegistryData(data []byte) (*Registry, error) {
	registry := &Registry{}
	if err := json.Unmarshal(data, registry); err != nil {
		return nil, fmt.Errorf("failed to parse registry data: %w", err)
	}
	return registry, nil
}

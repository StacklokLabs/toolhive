package registry

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/stacklok/toolhive/pkg/kubernetes/networking"
)

// RemoteRegistryProvider provides registry data from a remote HTTP endpoint
type RemoteRegistryProvider struct {
	registryURL    string
	allowPrivateIp bool
	registry       *Registry
	registryOnce   sync.Once
	registryErr    error
}

// NewRemoteRegistryProvider creates a new remote registry provider
func NewRemoteRegistryProvider(registryURL string, allowPrivateIp bool) *RemoteRegistryProvider {
	return &RemoteRegistryProvider{
		registryURL:    registryURL,
		allowPrivateIp: allowPrivateIp,
	}
}

// GetRegistry returns the remote registry data
func (p *RemoteRegistryProvider) GetRegistry() (*Registry, error) {
	p.registryOnce.Do(func() {
		client := networking.GetHttpClient(p.allowPrivateIp)
		resp, err := client.Get(p.registryURL)
		if err != nil {
			p.registryErr = fmt.Errorf("failed to fetch registry data from URL %s: %w", p.registryURL, err)
			return
		}
		defer resp.Body.Close()

		// Check if the response status code is OK
		if resp.StatusCode != http.StatusOK {
			p.registryErr = fmt.Errorf("response status code from URL %s not OK: status code %d", p.registryURL, resp.StatusCode)
			return
		}

		// Read the response body
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			p.registryErr = fmt.Errorf("failed to read registry data from response body: %w", err)
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
func (p *RemoteRegistryProvider) GetServer(name string) (*ImageMetadata, error) {
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
func (p *RemoteRegistryProvider) SearchServers(query string) ([]*ImageMetadata, error) {
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
func (p *RemoteRegistryProvider) ListServers() ([]*ImageMetadata, error) {
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

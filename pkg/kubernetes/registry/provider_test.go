package registry

import (
	"testing"

	"github.com/stacklok/toolhive/pkg/kubernetes/config"
)

func TestNewRegistryProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		config       *config.Config
		expectedType string
	}{
		{
			name:         "nil config returns embedded provider",
			config:       nil,
			expectedType: "*registry.EmbeddedRegistryProvider",
		},
		{
			name: "empty registry URL returns embedded provider",
			config: &config.Config{
				RegistryUrl: "",
			},
			expectedType: "*registry.EmbeddedRegistryProvider",
		},
		{
			name: "registry URL returns remote provider",
			config: &config.Config{
				RegistryUrl: "https://example.com/registry.json",
			},
			expectedType: "*registry.RemoteRegistryProvider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := NewRegistryProvider(tt.config)

			// Check the type of the provider
			providerType := getTypeName(provider)
			if providerType != tt.expectedType {
				t.Errorf("NewRegistryProvider() = %v, want %v", providerType, tt.expectedType)
			}
		})
	}
}

func TestEmbeddedRegistryProvider(t *testing.T) {
	t.Parallel()
	provider := NewEmbeddedRegistryProvider()

	// Test GetRegistry
	registry, err := provider.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry() error = %v", err)
	}

	if registry == nil {
		t.Fatal("GetRegistry() returned nil registry")
		return
	}

	if len(registry.Servers) == 0 {
		t.Error("GetRegistry() returned registry with no servers")
	}

	// Test that server names are set
	for name, server := range registry.Servers {
		if server.Name != name {
			t.Errorf("ImageMetadata name not set correctly: got %s, want %s", server.Name, name)
		}
	}

	// Test ListServers
	servers, err := provider.ListServers()
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}

	if len(servers) != len(registry.Servers) {
		t.Errorf("ListServers() returned %d servers, want %d", len(servers), len(registry.Servers))
	}

	// Test GetServer with existing server
	if len(servers) > 0 {
		firstServer := servers[0]
		server, err := provider.GetServer(firstServer.Name)
		if err != nil {
			t.Fatalf("GetServer() error = %v", err)
		}

		if server.Name != firstServer.Name {
			t.Errorf("GetServer() returned wrong server: got %s, want %s", server.Name, firstServer.Name)
		}
	}

	// Test GetServer with non-existing server
	_, err = provider.GetServer("non-existing-server")
	if err == nil {
		t.Error("GetServer() with non-existing server should return error")
	}
}

func TestRemoteRegistryProvider(t *testing.T) {
	t.Parallel()
	// Note: This test would require a mock HTTP server for full testing
	// For now, we just test the creation
	provider := NewRemoteRegistryProvider("https://example.com/registry.json", false)

	if provider == nil {
		t.Fatal("NewRemoteRegistryProvider() returned nil")
	}

	// Test that it implements the interface
	var _ Provider = provider
}

// getTypeName returns the type name of an interface value
func getTypeName(v interface{}) string {
	switch v.(type) {
	case *EmbeddedRegistryProvider:
		return "*registry.EmbeddedRegistryProvider"
	case *RemoteRegistryProvider:
		return "*registry.RemoteRegistryProvider"
	default:
		return "unknown"
	}
}

func TestGetRegistry(t *testing.T) {
	t.Parallel()
	provider, err := GetDefaultProvider()
	if err != nil {
		t.Fatalf("Failed to get registry provider: %v", err)
	}
	reg, err := provider.GetRegistry()
	if err != nil {
		t.Fatalf("Failed to get registry: %v", err)
	}

	if reg == nil {
		t.Fatal("Registry is nil")
		return
	}

	if reg.Version == "" {
		t.Error("Registry version is empty")
	}

	if reg.LastUpdated == "" {
		t.Error("Registry last updated is empty")
	}

	if len(reg.Servers) == 0 {
		t.Error("Registry has no servers")
	}
}

func TestGetServer(t *testing.T) {
	t.Parallel()
	// Test getting an existing server
	provider, err := GetDefaultProvider()
	if err != nil {
		t.Fatalf("Failed to get registry provider: %v", err)
	}
	server, err := provider.GetServer("brave-search")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}

	if server == nil {
		t.Fatal("ImageMetadata is nil")
		return
	}

	if server.Image == "" {
		t.Error("ImageMetadata image is empty")
	}

	if server.Description == "" {
		t.Error("ImageMetadata description is empty")
	}

	// Test getting a non-existent server
	_, err = provider.GetServer("non-existent-server")
	if err == nil {
		t.Error("Expected error when getting non-existent server")
	}
}

func TestSearchServers(t *testing.T) {
	t.Parallel()
	// Test searching for servers
	provider, err := GetDefaultProvider()
	if err != nil {
		t.Fatalf("Failed to get registry provider: %v", err)
	}
	servers, err := provider.SearchServers("search")
	if err != nil {
		t.Fatalf("Failed to search servers: %v", err)
	}

	if len(servers) == 0 {
		t.Error("No servers found for search query")
	}

	// Test searching for non-existent servers
	servers, err = provider.SearchServers("non-existent-server")
	if err != nil {
		t.Fatalf("Failed to search servers: %v", err)
	}

	if len(servers) > 0 {
		t.Errorf("Expected no servers for non-existent query, got %d", len(servers))
	}
}

func TestListServers(t *testing.T) {
	t.Parallel()
	provider, err := GetDefaultProvider()
	if err != nil {
		t.Fatalf("Failed to get registry provider: %v", err)
	}
	servers, err := provider.ListServers()
	if err != nil {
		t.Fatalf("Failed to list servers: %v", err)
	}

	if len(servers) == 0 {
		t.Error("No servers found")
	}

	// Verify that we get the same number of servers as in the registry
	reg, err := provider.GetRegistry()
	if err != nil {
		t.Fatalf("Failed to get registry: %v", err)
	}

	if len(servers) != len(reg.Servers) {
		t.Errorf("Expected %d servers, got %d", len(reg.Servers), len(servers))
	}
}

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

package registry

import (
	"sync"

	"github.com/stacklok/toolhive/pkg/kubernetes/config"
)

var (
	defaultProvider     Provider
	defaultProviderOnce sync.Once
	defaultProviderErr  error
)

// NewRegistryProvider creates a new registry provider based on the configuration
func NewRegistryProvider(cfg *config.Config) Provider {
	if cfg != nil && len(cfg.RegistryUrl) > 0 {
		return NewRemoteRegistryProvider(cfg.RegistryUrl, cfg.AllowPrivateRegistryIp)
	}
	return NewEmbeddedRegistryProvider()
}

// GetDefaultProvider returns the default registry provider instance
// This maintains backward compatibility with the existing singleton pattern
func GetDefaultProvider() (Provider, error) {
	defaultProviderOnce.Do(func() {
		cfg, err := config.LoadOrCreateConfig()
		if err != nil {
			defaultProviderErr = err
			return
		}
		defaultProvider = NewRegistryProvider(cfg)
	})

	return defaultProvider, defaultProviderErr
}

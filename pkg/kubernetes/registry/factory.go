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

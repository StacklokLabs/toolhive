package secrets

import (
	"context"
	"errors"
	"fmt"
)

// NoneManager is a no-op secrets provider that doesn't store or retrieve secrets.
// It's designed for use in Kubernetes environments where secrets are provided
// as environment variables or file mounts, eliminating the need for interactive
// password prompts.
type NoneManager struct{}

// GetSecret always returns an error indicating that the none provider doesn't support secret retrieval.
func (*NoneManager) GetSecret(_ context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("secret name cannot be empty")
	}
	return "", fmt.Errorf("secret not found: %s (none provider doesn't store secrets)", name)
}

// SetSecret always returns an error indicating that the none provider doesn't support secret storage.
func (*NoneManager) SetSecret(_ context.Context, name, _ string) error {
	if name == "" {
		return errors.New("secret name cannot be empty")
	}
	return errors.New("none provider doesn't support storing secrets")
}

// DeleteSecret always returns an error indicating that the none provider doesn't support secret deletion.
func (*NoneManager) DeleteSecret(_ context.Context, name string) error {
	if name == "" {
		return errors.New("secret name cannot be empty")
	}
	return fmt.Errorf("cannot delete non-existent secret: %s (none provider doesn't store secrets)", name)
}

// ListSecrets returns an empty list since the none provider doesn't store any secrets.
func (*NoneManager) ListSecrets(_ context.Context) ([]SecretDescription, error) {
	return []SecretDescription{}, nil
}

// Cleanup is a no-op for the none provider since there's nothing to clean up.
func (*NoneManager) Cleanup() error {
	return nil
}

// Capabilities returns the capabilities of the none provider.
// The none provider is essentially read-only but doesn't actually read anything.
func (*NoneManager) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		CanRead:    false,
		CanWrite:   false,
		CanDelete:  false,
		CanList:    true,
		CanCleanup: true,
	}
}

// NewNoneManager creates an instance of NoneManager.
func NewNoneManager() (Provider, error) {
	return &NoneManager{}, nil
}

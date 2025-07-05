package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNoneManager(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)
	assert.NotNil(t, manager)
}

func TestNoneManager_GetSecret(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)

	ctx := context.Background()

	// Test with valid name
	secret, err := manager.GetSecret(ctx, "test-secret")
	assert.Error(t, err)
	assert.Empty(t, secret)
	assert.Contains(t, err.Error(), "secret not found: test-secret")
	assert.Contains(t, err.Error(), "none provider doesn't store secrets")

	// Test with empty name
	secret, err = manager.GetSecret(ctx, "")
	assert.Error(t, err)
	assert.Empty(t, secret)
	assert.Contains(t, err.Error(), "secret name cannot be empty")
}

func TestNoneManager_SetSecret(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)

	ctx := context.Background()

	// Test with valid name and value
	err = manager.SetSecret(ctx, "test-secret", "test-value")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "none provider doesn't support storing secrets")

	// Test with empty name
	err = manager.SetSecret(ctx, "", "test-value")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "secret name cannot be empty")
}

func TestNoneManager_DeleteSecret(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)

	ctx := context.Background()

	// Test with valid name
	err = manager.DeleteSecret(ctx, "test-secret")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete non-existent secret: test-secret")
	assert.Contains(t, err.Error(), "none provider doesn't store secrets")

	// Test with empty name
	err = manager.DeleteSecret(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "secret name cannot be empty")
}

func TestNoneManager_ListSecrets(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)

	ctx := context.Background()

	secrets, err := manager.ListSecrets(ctx)
	assert.NoError(t, err)
	assert.Empty(t, secrets)
	assert.Equal(t, []SecretDescription{}, secrets)
}

func TestNoneManager_Cleanup(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)

	err = manager.Cleanup()
	assert.NoError(t, err)
}

func TestNoneManager_Capabilities(t *testing.T) {
	t.Parallel()
	manager, err := NewNoneManager()
	require.NoError(t, err)

	capabilities := manager.Capabilities()
	assert.False(t, capabilities.CanRead)
	assert.False(t, capabilities.CanWrite)
	assert.False(t, capabilities.CanDelete)
	assert.True(t, capabilities.CanList)
	assert.True(t, capabilities.CanCleanup)

	// Test capability helper methods
	assert.False(t, capabilities.IsReadOnly())
	assert.False(t, capabilities.IsReadWrite())
	assert.Equal(t, "custom", capabilities.String())
}

func TestCreateSecretProvider_None(t *testing.T) {
	t.Parallel()
	provider, err := CreateSecretProvider(NoneType)
	require.NoError(t, err)
	assert.NotNil(t, provider)

	// Verify it's actually a NoneManager
	_, ok := provider.(*NoneManager)
	assert.True(t, ok)
}

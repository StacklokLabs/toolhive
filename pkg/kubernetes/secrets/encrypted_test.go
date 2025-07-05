package secrets

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper functions specific to encrypted tests
func generateRandomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32) // 256 bits for AES-256
	_, err := rand.Read(key)
	require.NoError(t, err, "Generating a random key should not return an error")
	return key
}

func createEncryptedManager(t *testing.T, filePath string, key []byte) *EncryptedManager {
	t.Helper()
	manager, err := NewEncryptedManager(filePath, key)
	require.NoError(t, err, "Creating an EncryptedManager should not return an error")
	require.IsType(t, &EncryptedManager{}, manager, "The manager should be an EncryptedManager")
	return manager.(*EncryptedManager)
}

func TestEncryptedManager_GetSecret(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Create an EncryptedManager
	key := generateRandomKey(t)
	manager := createEncryptedManager(t, tempFile, key)

	// Test getting a non-existent secret
	_, err := manager.GetSecret(ctx, "non-existent")
	assert.Error(t, err, "Getting a non-existent secret should return an error")
	assert.Contains(t, err.Error(), "not found", "Error message should indicate the secret was not found")

	// Test getting a secret with an empty name
	_, err = manager.GetSecret(ctx, "")
	assert.Error(t, err, "Getting a secret with an empty name should return an error")
	assert.Contains(t, err.Error(), "cannot be empty", "Error message should indicate the name cannot be empty")

	// Set a secret
	err = manager.SetSecret(ctx, "test-key", "test-value")
	require.NoError(t, err, "Setting a secret should not return an error")

	// Test getting an existing secret
	value, err := manager.GetSecret(ctx, "test-key")
	assert.NoError(t, err, "Getting an existing secret should not return an error")
	assert.Equal(t, "test-value", value, "The retrieved value should match the set value")
}

func TestEncryptedManager_SetSecret(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Create an EncryptedManager
	key := generateRandomKey(t)
	manager := createEncryptedManager(t, tempFile, key)

	// Test setting a secret with an empty name
	err := manager.SetSecret(ctx, "", "test-value")
	assert.Error(t, err, "Setting a secret with an empty name should return an error")
	assert.Contains(t, err.Error(), "cannot be empty", "Error message should indicate the name cannot be empty")

	// Test setting a new secret
	err = manager.SetSecret(ctx, "test-key", "test-value")
	assert.NoError(t, err, "Setting a new secret should not return an error")

	// Verify the secret was set
	value, err := manager.GetSecret(ctx, "test-key")
	assert.NoError(t, err, "Getting the set secret should not return an error")
	assert.Equal(t, "test-value", value, "The retrieved value should match the set value")

	// Test updating an existing secret
	err = manager.SetSecret(ctx, "test-key", "updated-value")
	assert.NoError(t, err, "Updating an existing secret should not return an error")

	// Verify the secret was updated
	value, err = manager.GetSecret(ctx, "test-key")
	assert.NoError(t, err, "Getting the updated secret should not return an error")
	assert.Equal(t, "updated-value", value, "The retrieved value should match the updated value")

	// Verify the file was updated by creating a new manager with the same key and file
	newManager := createEncryptedManager(t, tempFile, key)
	value, err = newManager.GetSecret(ctx, "test-key")
	assert.NoError(t, err, "Getting the secret from a new manager should not return an error")
	assert.Equal(t, "updated-value", value, "The retrieved value should match the updated value")
}

func TestEncryptedManager_DeleteSecret(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Create an EncryptedManager
	key := generateRandomKey(t)
	manager := createEncryptedManager(t, tempFile, key)

	// Test deleting a non-existent secret
	err := manager.DeleteSecret(ctx, "non-existent")
	assert.Error(t, err, "Deleting a non-existent secret should return an error")
	assert.Contains(t, err.Error(), "cannot delete non-existent", "Error message should indicate the secret does not exist")

	// Test deleting a secret with an empty name
	err = manager.DeleteSecret(ctx, "")
	assert.Error(t, err, "Deleting a secret with an empty name should return an error")
	assert.Contains(t, err.Error(), "cannot be empty", "Error message should indicate the name cannot be empty")

	// Set a secret
	err = manager.SetSecret(ctx, "test-key", "test-value")
	require.NoError(t, err, "Setting a secret should not return an error")

	// Test deleting an existing secret
	err = manager.DeleteSecret(ctx, "test-key")
	assert.NoError(t, err, "Deleting an existing secret should not return an error")

	// Verify the secret was deleted
	_, err = manager.GetSecret(ctx, "test-key")
	assert.Error(t, err, "Getting a deleted secret should return an error")
	assert.Contains(t, err.Error(), "not found", "Error message should indicate the secret was not found")

	// Verify the file was updated by creating a new manager with the same key and file
	newManager := createEncryptedManager(t, tempFile, key)
	_, err = newManager.GetSecret(ctx, "test-key")
	assert.Error(t, err, "Getting a deleted secret from a new manager should return an error")
	assert.Contains(t, err.Error(), "not found", "Error message should indicate the secret was not found")
}

func TestEncryptedManager_ListSecrets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Create an EncryptedManager
	key := generateRandomKey(t)
	manager := createEncryptedManager(t, tempFile, key)

	// Test listing secrets when there are none
	secrets, err := manager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets should not return an error")
	assert.Empty(t, secrets, "There should be no secrets initially")

	// Set some secrets
	require.NoError(t, manager.SetSecret(ctx, "key1", "value1"), "Setting a secret should not return an error")
	require.NoError(t, manager.SetSecret(ctx, "key2", "value2"), "Setting a secret should not return an error")
	require.NoError(t, manager.SetSecret(ctx, "key3", "value3"), "Setting a secret should not return an error")

	// Test listing secrets
	secrets, err = manager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets should not return an error")
	assert.Len(t, secrets, 3, "There should be 3 secrets")

	// Helper function to check if a key exists in the secrets list
	containsKey := func(key string) bool {
		for _, secret := range secrets {
			if secret.Key == key {
				return true
			}
		}
		return false
	}

	assert.True(t, containsKey("key1"), "The list should contain key1")
	assert.True(t, containsKey("key2"), "The list should contain key2")
	assert.True(t, containsKey("key3"), "The list should contain key3")

	// Verify the file was updated by creating a new manager with the same key and file
	newManager := createEncryptedManager(t, tempFile, key)
	secrets, err = newManager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets from a new manager should not return an error")
	assert.Len(t, secrets, 3, "There should be 3 secrets")

	// Helper function to check if a key exists in the secrets list
	containsKeyInNewManager := func(key string) bool {
		for _, secret := range secrets {
			if secret.Key == key {
				return true
			}
		}
		return false
	}

	assert.True(t, containsKeyInNewManager("key1"), "The list should contain key1")
	assert.True(t, containsKeyInNewManager("key2"), "The list should contain key2")
	assert.True(t, containsKeyInNewManager("key3"), "The list should contain key3")
}

func TestEncryptedManager_Cleanup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Create an EncryptedManager
	key := generateRandomKey(t)
	manager := createEncryptedManager(t, tempFile, key)

	// Set some secrets
	require.NoError(t, manager.SetSecret(ctx, "key1", "value1"), "Setting a secret should not return an error")
	require.NoError(t, manager.SetSecret(ctx, "key2", "value2"), "Setting a secret should not return an error")

	// Verify the secrets were set
	secrets, err := manager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets should not return an error")
	assert.Len(t, secrets, 2, "There should be 2 secrets")

	// Test cleaning up all secrets
	err = manager.Cleanup()
	assert.NoError(t, err, "Cleaning up should not return an error")

	// Verify all secrets were removed
	secrets, err = manager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets should not return an error")
	assert.Empty(t, secrets, "There should be no secrets after cleanup")

	// Verify the file was updated by creating a new manager with the same key and file
	newManager := createEncryptedManager(t, tempFile, key)
	secrets, err = newManager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets from a new manager should not return an error")
	assert.Empty(t, secrets, "There should be no secrets after cleanup")
}

func TestNewEncryptedManager(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Generate a random key
	key := generateRandomKey(t)

	// Test creating an EncryptedManager with a valid file path and key
	manager, err := NewEncryptedManager(tempFile, key)
	assert.NoError(t, err, "Creating an EncryptedManager with a valid file path and key should not return an error")
	assert.NotNil(t, manager, "The manager should not be nil")
	assert.IsType(t, &EncryptedManager{}, manager, "The manager should be an EncryptedManager")

	// Test creating an EncryptedManager with a non-existent directory
	nonExistentFile := filepath.Join(os.TempDir(), "non-existent-dir", "secrets.json")
	_, err = NewEncryptedManager(nonExistentFile, key)
	assert.Error(t, err, "Creating an EncryptedManager with a non-existent directory should return an error")
	assert.Contains(t, err.Error(), "failed to open secrets file", "Error message should indicate the file could not be opened")

	// Test creating an EncryptedManager with an empty key
	_, err = NewEncryptedManager(tempFile, nil)
	assert.Error(t, err, "Creating an EncryptedManager with an empty key should return an error")
	assert.Contains(t, err.Error(), "key cannot be empty", "Error message should indicate the key cannot be empty")

	// Test creating an EncryptedManager with an existing file that contains valid encrypted data
	// First, create a manager and add a secret
	manager, err = NewEncryptedManager(tempFile, key)
	require.NoError(t, err, "Creating an EncryptedManager should not return an error")
	err = manager.SetSecret(ctx, "test-key", "test-value")
	require.NoError(t, err, "Setting a secret should not return an error")

	// Now create a new manager with the same file and key
	newManager, err := NewEncryptedManager(tempFile, key)
	assert.NoError(t, err, "Creating an EncryptedManager with an existing file should not return an error")
	assert.NotNil(t, newManager, "The manager should not be nil")

	// Verify the secret was loaded
	value, err := newManager.GetSecret(ctx, "test-key")
	assert.NoError(t, err, "Getting the secret should not return an error")
	assert.Equal(t, "test-value", value, "The retrieved value should match the set value")

	// Test creating an EncryptedManager with an existing file but wrong key
	wrongKey := generateRandomKey(t)
	_, err = NewEncryptedManager(tempFile, wrongKey)
	assert.Error(t, err, "Creating an EncryptedManager with a wrong key should return an error")
	assert.Contains(t, err.Error(), "unable to decrypt", "Error message should indicate decryption failed")
}

func TestEncryptedManager_Concurrency(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Create a temporary file for testing
	tempFile := createTempFile(t)
	defer os.Remove(tempFile)

	// Create an EncryptedManager
	key := generateRandomKey(t)
	manager := createEncryptedManager(t, tempFile, key)

	// Set a secret
	err := manager.SetSecret(ctx, "test-key", "test-value")
	require.NoError(t, err, "Setting a secret should not return an error")

	// Test concurrent access to the manager
	// This is a basic test that just ensures no race conditions occur
	// For a more thorough test, we would need to use the race detector
	const numGoroutines = 10
	done := make(chan bool)

	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			// Get the secret
			value, err := manager.GetSecret(ctx, "test-key")
			assert.NoError(t, err, "Getting the secret should not return an error")
			assert.Equal(t, "test-value", value, "The retrieved value should match the set value")

			// Set a new secret
			err = manager.SetSecret(ctx, fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
			assert.NoError(t, err, "Setting a secret should not return an error")

			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all secrets were set
	secrets, err := manager.ListSecrets(ctx)
	assert.NoError(t, err, "Listing secrets should not return an error")
	assert.Len(t, secrets, numGoroutines+1, "There should be numGoroutines+1 secrets")

	// Helper function to check if a key exists in the secrets list
	containsKey := func(key string) bool {
		for _, secret := range secrets {
			if secret.Key == key {
				return true
			}
		}
		return false
	}

	// Check if the original key exists
	assert.True(t, containsKey("test-key"), "The list should contain the original key")

	// Check if all the keys created in the goroutines exist
	for i := 0; i < numGoroutines; i++ {
		keyName := fmt.Sprintf("key-%d", i)
		assert.True(t, containsKey(keyName), "The list should contain %s", keyName)
	}
}

// End of tests

// Helper functions
func createTempFile(t *testing.T) string {
	t.Helper()
	tempFile, err := os.CreateTemp("", "secrets-test-*.json")
	require.NoError(t, err, "Creating a temporary file should not return an error")
	tempFile.Close()
	return tempFile.Name()
}

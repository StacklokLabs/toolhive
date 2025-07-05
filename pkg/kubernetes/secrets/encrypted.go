package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"golang.org/x/sync/syncmap"

	"github.com/stacklok/toolhive/pkg/kubernetes/secrets/aes"
)

// EncryptedManager stores secrets in an encrypted file.
// AES-256-GCM is used for encryption.
type EncryptedManager struct {
	filePath string
	// Key used to re-encrypt the secrets file if changes are needed.
	key     []byte
	secrets syncmap.Map // Thread-safe map for storing secrets
}

// fileStructure is the structure of the secrets file.
type fileStructure struct {
	Secrets map[string]string `json:"secrets"`
}

// GetSecret retrieves a secret from the secret store.
func (e *EncryptedManager) GetSecret(_ context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("secret name cannot be empty")
	}

	value, ok := e.secrets.Load(name)
	if !ok {
		return "", fmt.Errorf("secret not found: %s", name)
	}
	return value.(string), nil
}

// SetSecret stores a secret in the secret store.
func (e *EncryptedManager) SetSecret(_ context.Context, name, value string) error {
	if name == "" {
		return errors.New("secret name cannot be empty")
	}

	e.secrets.Store(name, value)
	return e.updateFile()
}

// DeleteSecret removes a secret from the secret store.
func (e *EncryptedManager) DeleteSecret(_ context.Context, name string) error {
	if name == "" {
		return errors.New("secret name cannot be empty")
	}

	// Check if the secret exists first
	_, ok := e.secrets.Load(name)
	if !ok {
		return fmt.Errorf("cannot delete non-existent secret: %s", name)
	}

	e.secrets.Delete(name)
	return e.updateFile()
}

// ListSecrets returns a list of all secret names stored in the manager.
func (e *EncryptedManager) ListSecrets(_ context.Context) ([]SecretDescription, error) {
	var secretNames []SecretDescription

	e.secrets.Range(func(key, _ interface{}) bool {
		secretNames = append(secretNames, SecretDescription{Key: key.(string)})
		return true
	})

	return secretNames, nil
}

// Cleanup removes all secrets managed by this manager.
func (e *EncryptedManager) Cleanup() error {
	// Create a new empty syncmap.Map
	e.secrets = syncmap.Map{}

	// Update the file to reflect the empty state
	return e.updateFile()
}

// Capabilities returns the capabilities of the encrypted provider.
func (*EncryptedManager) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		CanRead:    true,
		CanWrite:   true,
		CanDelete:  true,
		CanList:    true,
		CanCleanup: true,
	}
}

func (e *EncryptedManager) updateFile() error {
	// Convert syncmap.Map to map[string]string for JSON marshaling
	secretsMap := make(map[string]string)
	e.secrets.Range(func(key, value interface{}) bool {
		secretsMap[key.(string)] = value.(string)
		return true
	})

	contents, err := json.Marshal(fileStructure{Secrets: secretsMap})
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}

	encryptedContents, err := aes.Encrypt(contents, e.key)
	if err != nil {
		return fmt.Errorf("failed to encrypt secrets: %w", err)
	}

	err = os.WriteFile(e.filePath, encryptedContents, 0600)
	if err != nil {
		return fmt.Errorf("failed to write secrets to file: %w", err)
	}
	return nil
}

// NewEncryptedManager creates an instance of EncryptedManager.
func NewEncryptedManager(filePath string, key []byte) (Provider, error) {
	if len(key) == 0 {
		return nil, errors.New("key cannot be empty")
	}

	filePath = path.Clean(filePath)
	// #nosec G304: File path is not configurable at this time.
	secretsFile, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open secrets file: %w", err)
	}
	defer secretsFile.Close()

	stat, err := secretsFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat secrets file: %w", err)
	}

	// Create a new EncryptedManager with an empty syncmap.Map
	manager := &EncryptedManager{
		filePath: filePath,
		secrets:  syncmap.Map{},
		key:      key,
	}

	// If the file is not empty, load the secrets into the syncmap.Map
	if stat.Size() > 0 {
		// Attempt to load encrypted contents and decrypt them
		encryptedContents, err := io.ReadAll(secretsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read secrets file: %w", err)
		}
		decryptedContents, err := aes.Decrypt(encryptedContents, key)
		if err != nil {
			return nil, fmt.Errorf("unable to decrypt secrets file: %w", err)
		}

		var contents fileStructure
		err = json.Unmarshal(decryptedContents, &contents)
		if err != nil {
			return nil, fmt.Errorf("failed to decode secrets file: %w", err)
		}

		// Store each secret in the syncmap.Map
		for key, value := range contents.Secrets {
			manager.secrets.Store(key, value)
		}
	}

	return manager, nil
}

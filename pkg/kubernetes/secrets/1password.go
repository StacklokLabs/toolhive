package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/secrets/clients"
)

//go:generate mockgen -destination=mocks/mock_onepassword.go -package=mocks -source=1password.go OPSecretsService

// Err1PasswordReadOnly indicates that the 1Password secrets manager is read-only.
// Is it returned by operations which attempt to change values in 1Password.
var Err1PasswordReadOnly = fmt.Errorf("1Password secrets manager is read-only, write operations are not supported")

// OnePasswordManager manages secrets in 1Password.
type OnePasswordManager struct {
	client clients.OnePasswordClient
}

var timeout = 5 * time.Second

// GetSecret retrieves a secret from 1Password.
func (o *OnePasswordManager) GetSecret(ctx context.Context, path string) (string, error) {
	if !strings.Contains(path, "op://") {
		return "", fmt.Errorf("invalid secret path: %s", path)
	}

	secret, err := o.client.Resolve(ctx, path)
	if err != nil {
		return "", fmt.Errorf("error resolving secret: %v", err)
	}

	return secret, nil
}

// SetSecret is not supported for 1Password unless there is
// demand for it.
func (*OnePasswordManager) SetSecret(_ context.Context, _, _ string) error {
	return Err1PasswordReadOnly
}

// DeleteSecret is not supported for 1Password unless there is
// demand for it.
func (*OnePasswordManager) DeleteSecret(_ context.Context, _ string) error {
	return Err1PasswordReadOnly
}

// ListSecrets lists the paths to the secrets in 1Password.
// 1Password has a hierarchy of vaults, items, and fields.
// Each secret is represented as a path in the format:
// op://<vault>/<item>/<field>
func (o *OnePasswordManager) ListSecrets(ctx context.Context) ([]SecretDescription, error) {
	// First, grab the list of vaults we have access to.
	vaults, err := o.client.ListVaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving vaults from 1password API: %v", err)
	}

	var secrets []SecretDescription
	// For each vault...
	for _, vault := range vaults {
		items, err := o.client.ListItems(ctx, vault.ID)
		if err != nil {
			return nil, fmt.Errorf("error retrieving secrets from 1password API: %v", err)
		}

		// For each item in the vault...
		for _, item := range items {
			details, err := o.client.GetItem(ctx, vault.ID, item.ID)
			if err != nil {
				return nil, fmt.Errorf("error retrieving item details from 1password API: %v", err)
			}
			// For each field in the item...
			for _, field := range details.Fields {
				// Create a path and human-readable name for each field.
				description := SecretDescription{
					Key:         fmt.Sprintf("op://%s/%s/%s", item.VaultID, item.ID, field.ID),
					Description: fmt.Sprintf("%s :: %s :: %s", vault.Title, item.Title, field.Title),
				}
				secrets = append(secrets, description)
			}
		}
	}

	return secrets, nil
}

// Cleanup is not needed for 1Password.
func (*OnePasswordManager) Cleanup() error {
	return nil
}

// Capabilities returns the capabilities of the 1Password provider.
// Read-only provider with listing support.
func (*OnePasswordManager) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		CanRead:    true,
		CanWrite:   false, // 1Password is read-only for now
		CanDelete:  false, // 1Password is read-only for now
		CanList:    true,  // Listing is now supported
		CanCleanup: false, // Not applicable for 1Password
	}
}

// NewOnePasswordManager creates an instance of OnePasswordManager.
func NewOnePasswordManager() (Provider, error) {
	token := os.Getenv("OP_SERVICE_ACCOUNT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("OP_SERVICE_ACCOUNT_TOKEN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := clients.NewOnePasswordClient(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("error creating 1Password client: %v", err)
	}

	return &OnePasswordManager{
		client: client,
	}, nil
}

// NewOnePasswordManagerWithClient creates an instance of OnePasswordManager with a provided 1password client.
// This function is primarily intended for testing purposes.
func NewOnePasswordManagerWithClient(client clients.OnePasswordClient) *OnePasswordManager {
	return &OnePasswordManager{
		client: client,
	}
}

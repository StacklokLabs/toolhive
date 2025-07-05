// Package clients contains code for connecting to secret provider APIs.
package clients

import (
	"context"
	"fmt"

	"github.com/1password/onepassword-sdk-go"
)

//go:generate mockgen -destination=mocks/mock_onepassword.go -package=mocks -source=1password.go OnePasswordClient

// OnePasswordClient defines the subset of the 1Password SDK that we use.
type OnePasswordClient interface {
	Resolve(ctx context.Context, secretReference string) (string, error)
	ListItems(ctx context.Context, vaultID string, filters ...onepassword.ItemListFilter) ([]onepassword.ItemOverview, error)
	ListVaults(ctx context.Context) ([]onepassword.VaultOverview, error)
	GetItem(ctx context.Context, vaultID, itemID string) (onepassword.Item, error)
}

// NewOnePasswordClient creates a OnePasswordClient from the 1Password SDK
func NewOnePasswordClient(ctx context.Context, token string) (OnePasswordClient, error) {
	client, err := onepassword.NewClient(
		ctx,
		onepassword.WithServiceAccountToken(token),
		onepassword.WithIntegrationInfo(onepassword.DefaultIntegrationName, onepassword.DefaultIntegrationVersion),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating 1Password client: %v", err)
	}

	return &onePasswordClient{client: client}, nil
}

// defaultOnePasswordClient implements the OnePasswordClient interface.
// Note that the methods we need are from two different interfaces in the SDK.
// This implementation presents them in a single interface for ease of mocking.
type onePasswordClient struct {
	client *onepassword.Client
}

func (opc *onePasswordClient) Resolve(ctx context.Context, secretReference string) (string, error) {
	secret, err := opc.client.Secrets().Resolve(ctx, secretReference)
	if err != nil {
		return "", fmt.Errorf("error resolving secret: %v", err)
	}
	return secret, nil
}

func (opc *onePasswordClient) ListItems(
	ctx context.Context,
	vaultID string,
	filters ...onepassword.ItemListFilter,
) ([]onepassword.ItemOverview, error) {
	return opc.client.Items().List(ctx, vaultID, filters...)
}

func (opc *onePasswordClient) ListVaults(ctx context.Context) ([]onepassword.VaultOverview, error) {
	return opc.client.Vaults().List(ctx)
}

func (opc *onePasswordClient) GetItem(ctx context.Context, vaultID, itemID string) (onepassword.Item, error) {
	return opc.client.Items().Get(ctx, vaultID, itemID)
}

package secrets_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/1password/onepassword-sdk-go"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/stacklok/toolhive/pkg/kubernetes/secrets"
	cm "github.com/stacklok/toolhive/pkg/kubernetes/secrets/clients/mocks"
)

func TestNewOnePasswordManager(t *testing.T) {
	t.Parallel()
	t.Run("missing token", func(t *testing.T) {
		t.Parallel()
		// Make sure token is not set
		os.Unsetenv("OP_SERVICE_ACCOUNT_TOKEN")

		manager, err := secrets.NewOnePasswordManager()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "OP_SERVICE_ACCOUNT_TOKEN is not set")
		assert.Nil(t, manager)
	})
}

func TestOnePasswordManager_GetSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		setupMock   func(mockClient *cm.MockOnePasswordClient)
		wantSecret  string
		wantErr     bool
		errContains string
	}{
		{
			name:        "invalid path format",
			path:        "invalid-path",
			setupMock:   func(*cm.MockOnePasswordClient) {},
			wantSecret:  "",
			wantErr:     true,
			errContains: "invalid secret path",
		},
		{
			name: "valid path format with success",
			path: "op://vault/item/field",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					Resolve(gomock.Any(), "op://vault/item/field").
					Return("test-secret-value", nil)
			},
			wantSecret:  "test-secret-value",
			wantErr:     false,
			errContains: "",
		},
		{
			name: "valid path format with error",
			path: "op://vault/item/field",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					Resolve(gomock.Any(), "op://vault/item/field").
					Return("", fmt.Errorf("secret not found"))
			},
			wantSecret:  "",
			wantErr:     true,
			errContains: "error resolving secret",
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable for parallel execution
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // Enable parallel execution
			ctx := t.Context()

			// Create a new mock controller for each test case
			ctrl := gomock.NewController(t)
			t.Cleanup(func() { ctrl.Finish() })

			// Create a new mock client for each test case
			mockClient := cm.NewMockOnePasswordClient(ctrl)

			// Create a new manager with the mock client
			manager := secrets.NewOnePasswordManagerWithClient(mockClient)

			// Setup expectations
			tt.setupMock(mockClient)

			// Execute test
			secret, err := manager.GetSecret(ctx, tt.path)

			// Assert results
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantSecret, secret)
			}
		})
	}
}

func TestOnePasswordManager_ListSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setupMock   func(mockClient *cm.MockOnePasswordClient)
		wantSecrets []secrets.SecretDescription
		wantErr     bool
		errContains string
	}{
		{
			name: "successful listing with multiple vaults and items",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				// Mock ListVaults
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return([]onepassword.VaultOverview{
						{ID: "vault1", Title: "Vault One"},
						{ID: "vault2", Title: "Vault Two"},
					}, nil)

				// Mock ListItems for vault1
				mockClient.EXPECT().
					ListItems(gomock.Any(), "vault1", gomock.Any()).
					Return([]onepassword.ItemOverview{
						{ID: "item1", Title: "Item One", VaultID: "vault1"},
						{ID: "item2", Title: "Item Two", VaultID: "vault1"},
					}, nil)

				// Mock ListItems for vault2
				mockClient.EXPECT().
					ListItems(gomock.Any(), "vault2", gomock.Any()).
					Return([]onepassword.ItemOverview{
						{ID: "item3", Title: "Item Three", VaultID: "vault2"},
					}, nil)

				// Mock GetItem for each item
				mockClient.EXPECT().
					GetItem(gomock.Any(), "vault1", "item1").
					Return(onepassword.Item{
						ID:    "item1",
						Title: "Item One",
						Fields: []onepassword.ItemField{
							{ID: "field1", Title: "Field One"},
							{ID: "field2", Title: "Field Two"},
						},
					}, nil)

				mockClient.EXPECT().
					GetItem(gomock.Any(), "vault1", "item2").
					Return(onepassword.Item{
						ID:    "item2",
						Title: "Item Two",
						Fields: []onepassword.ItemField{
							{ID: "field3", Title: "Field Three"},
						},
					}, nil)

				mockClient.EXPECT().
					GetItem(gomock.Any(), "vault2", "item3").
					Return(onepassword.Item{
						ID:    "item3",
						Title: "Item Three",
						Fields: []onepassword.ItemField{
							{ID: "field4", Title: "Field Four"},
						},
					}, nil)
			},
			wantSecrets: []secrets.SecretDescription{
				{Key: "op://vault1/item1/field1", Description: "Vault One :: Item One :: Field One"},
				{Key: "op://vault1/item1/field2", Description: "Vault One :: Item One :: Field Two"},
				{Key: "op://vault1/item2/field3", Description: "Vault One :: Item Two :: Field Three"},
				{Key: "op://vault2/item3/field4", Description: "Vault Two :: Item Three :: Field Four"},
			},
			wantErr:     false,
			errContains: "",
		},
		{
			name: "empty vaults list",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return([]onepassword.VaultOverview{}, nil)
			},
			wantSecrets: []secrets.SecretDescription{},
			wantErr:     false,
			errContains: "",
		},
		{
			name: "vault with no items",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return([]onepassword.VaultOverview{
						{ID: "vault1", Title: "Vault One"},
					}, nil)

				mockClient.EXPECT().
					ListItems(gomock.Any(), "vault1", gomock.Any()).
					Return([]onepassword.ItemOverview{}, nil)
			},
			wantSecrets: []secrets.SecretDescription{},
			wantErr:     false,
			errContains: "",
		},
		{
			name: "item with no fields",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return([]onepassword.VaultOverview{
						{ID: "vault1", Title: "Vault One"},
					}, nil)

				mockClient.EXPECT().
					ListItems(gomock.Any(), "vault1", gomock.Any()).
					Return([]onepassword.ItemOverview{
						{ID: "item1", Title: "Item One", VaultID: "vault1"},
					}, nil)

				mockClient.EXPECT().
					GetItem(gomock.Any(), "vault1", "item1").
					Return(onepassword.Item{
						ID:     "item1",
						Title:  "Item One",
						Fields: []onepassword.ItemField{},
					}, nil)
			},
			wantSecrets: []secrets.SecretDescription{},
			wantErr:     false,
			errContains: "",
		},
		{
			name: "error listing vaults",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return(nil, fmt.Errorf("connection error"))
			},
			wantSecrets: nil,
			wantErr:     true,
			errContains: "error retrieving vaults from 1password API",
		},
		{
			name: "error listing items",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return([]onepassword.VaultOverview{
						{ID: "vault1", Title: "Vault One"},
					}, nil)

				mockClient.EXPECT().
					ListItems(gomock.Any(), "vault1", gomock.Any()).
					Return(nil, fmt.Errorf("connection error"))
			},
			wantSecrets: nil,
			wantErr:     true,
			errContains: "error retrieving secrets from 1password API",
		},
		{
			name: "error getting item details",
			setupMock: func(mockClient *cm.MockOnePasswordClient) {
				mockClient.EXPECT().
					ListVaults(gomock.Any()).
					Return([]onepassword.VaultOverview{
						{ID: "vault1", Title: "Vault One"},
					}, nil)

				mockClient.EXPECT().
					ListItems(gomock.Any(), "vault1", gomock.Any()).
					Return([]onepassword.ItemOverview{
						{ID: "item1", Title: "Item One", VaultID: "vault1"},
					}, nil)

				mockClient.EXPECT().
					GetItem(gomock.Any(), "vault1", "item1").
					Return(onepassword.Item{}, fmt.Errorf("connection error"))
			},
			wantSecrets: nil,
			wantErr:     true,
			errContains: "error retrieving item details from 1password API",
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable for parallel execution
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // Enable parallel execution
			ctx := t.Context()

			// Create a new mock controller for each test case
			ctrl := gomock.NewController(t)
			t.Cleanup(func() { ctrl.Finish() })

			// Create a new mock client for each test case
			mockClient := cm.NewMockOnePasswordClient(ctrl)

			// Create a new manager with the mock client
			manager := secrets.NewOnePasswordManagerWithClient(mockClient)

			// Setup expectations
			tt.setupMock(mockClient)

			// Execute test
			secrets, err := manager.ListSecrets(ctx)

			// Assert results
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.wantSecrets, secrets)
			}
		})
	}
}

func TestOnePasswordManager_UnsupportedOperations(t *testing.T) {
	t.Parallel()
	// Create a mock controller
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })

	// Create mock client
	mockClient := cm.NewMockOnePasswordClient(ctrl)
	manager := secrets.NewOnePasswordManagerWithClient(mockClient)

	t.Run("SetSecret", func(t *testing.T) {
		t.Parallel()
		err := manager.SetSecret(t.Context(), "test", "value")
		assert.Error(t, err, secrets.Err1PasswordReadOnly)
	})

	t.Run("DeleteSecret", func(t *testing.T) {
		t.Parallel()
		err := manager.DeleteSecret(t.Context(), "test")
		assert.Error(t, err, secrets.Err1PasswordReadOnly)
	})

	t.Run("Cleanup", func(t *testing.T) {
		t.Parallel()
		err := manager.Cleanup()
		assert.NoError(t, err, "Cleanup should return nil as it's not supported")
	})
}

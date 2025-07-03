package app

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/stacklok/toolhive/pkg/config"
	"github.com/stacklok/toolhive/pkg/secrets"
)

// AddOIDCFlags adds OIDC validation flags to the provided command.
func AddOIDCFlags(cmd *cobra.Command) {
	cmd.Flags().String("oidc-issuer", "", "OIDC issuer URL (e.g., https://accounts.google.com)")
	cmd.Flags().String("oidc-audience", "", "Expected audience for the token")
	cmd.Flags().String("oidc-jwks-url", "", "URL to fetch the JWKS from")
	cmd.Flags().String("oidc-client-id", "", "OIDC client ID")
	cmd.Flags().Bool("oidc-skip-opaque-token-validation", false, "Allow skipping validation of opaque tokens")
}

// GetStringFlagOrEmpty tries to get the string value of the given flag.
// If the flag doesn't exist or there's an error, it returns an empty string.
func GetStringFlagOrEmpty(cmd *cobra.Command, flagName string) string {
	value, err := cmd.Flags().GetString(flagName)
	if err != nil {
		return ""
	}
	return value
}

// IsOIDCEnabled returns true if OIDC validation is enabled for the given command.
// OIDC validation is considered enabled if either the OIDC issuer or the JWKS URL flag is provided.
func IsOIDCEnabled(cmd *cobra.Command) bool {
	jwksURL := GetStringFlagOrEmpty(cmd, "oidc-jwks-url")
	issuer := GetStringFlagOrEmpty(cmd, "oidc-issuer")

	return jwksURL != "" || issuer != ""
}

// SetSecretsProvider sets the secrets provider type in the configuration.
// It validates the input, tests the provider functionality, and updates the configuration.
// Choices are `encrypted`, `1password`, and `none`.
func SetSecretsProvider(provider secrets.ProviderType) error {
	// Validate input
	if provider == "" {
		fmt.Println("validation error: provider cannot be empty")
		return fmt.Errorf("validation error: provider cannot be empty")
	}

	// Validate the provider type
	switch provider {
	case secrets.EncryptedType:
	case secrets.OnePasswordType:
	case secrets.NoneType:
		// Valid provider type
	default:
		return fmt.Errorf("invalid secrets provider type: %s (valid types: %s, %s, %s)",
			provider, string(secrets.EncryptedType), string(secrets.OnePasswordType), string(secrets.NoneType))
	}

	// Validate that the provider can be created and works correctly
	ctx := context.Background()
	result := secrets.ValidateProvider(ctx, provider)
	if !result.Success {
		return fmt.Errorf("provider validation failed: %w", result.Error)
	}

	// Update the secrets provider type and mark setup as completed
	err := config.UpdateConfig(func(c *config.Config) {
		c.Secrets.ProviderType = string(provider)
		c.Secrets.SetupCompleted = true
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Secrets provider type updated to: %s\n", provider)
	return nil
}

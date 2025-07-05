package app

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/stacklok/toolhive/pkg/config"
	"github.com/stacklok/toolhive/pkg/secrets"
	"github.com/stacklok/toolhive/pkg/workloads"
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

// completeMCPServerNames provides completion for MCP server names.
// This function is used by commands like 'rm' and 'stop' to auto-complete
// container names with available MCP servers.
func completeMCPServerNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	// Only complete the first argument (container name)
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := cmd.Context()

	// Create container manager
	manager, err := workloads.NewManager(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// List all workloads (including stopped ones for rm command, only running for stop)
	// We'll include all workloads since rm can remove stopped containers too
	workloadList, err := manager.ListWorkloads(ctx, true)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// Extract workload names for completion
	var names []string
	for _, workload := range workloadList {
		names = append(names, workload.Name)
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeLogsArgs provides completion for the logs command.
// This function completes both MCP server names and the special "prune" argument.
func completeLogsArgs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	// Only complete the first argument
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := cmd.Context()

	// Create container manager
	manager, err := workloads.NewManager(ctx)
	if err != nil {
		return []string{"prune"}, cobra.ShellCompDirectiveNoFileComp
	}

	// List all workloads (including stopped ones)
	workloadList, err := manager.ListWorkloads(ctx, true)
	if err != nil {
		return []string{"prune"}, cobra.ShellCompDirectiveNoFileComp
	}

	// Extract workload names and add "prune" option
	var completions []string
	completions = append(completions, "prune")
	for _, workload := range workloadList {
		completions = append(completions, workload.Name)
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

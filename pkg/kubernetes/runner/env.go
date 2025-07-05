package runner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/stacklok/toolhive/pkg/kubernetes/config"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/registry"
	"github.com/stacklok/toolhive/pkg/kubernetes/secrets"
)

// EnvVarValidator defines the interface for checking that the expected
// environment variables and secrets have been supplied when creating a
// workload. This is implemented as a strategy pattern since the handling
// is different for the CLI vs the API and k8s.
type EnvVarValidator interface {
	// Validate checks that all required environment variables and secrets are provided
	// and returns the processed environment variables to be set.
	Validate(
		ctx context.Context,
		metadata *registry.ImageMetadata,
		runConfig *RunConfig,
		suppliedEnvVars []string,
	) ([]string, error)
}

// DetachedEnvVarValidator implements the EnvVarValidator interface for
// scenarios where the user cannot be prompted for input. Any missing,
// mandatory variables will result in an error being returned.
type DetachedEnvVarValidator struct{}

// Validate checks that all required environment variables and secrets are provided
// and returns the processed environment variables to be set.
func (*DetachedEnvVarValidator) Validate(
	_ context.Context,
	metadata *registry.ImageMetadata,
	runConfig *RunConfig,
	suppliedEnvVars []string,
) ([]string, error) {
	// Check variables in metadata if we are processing an image from our registry.
	if metadata != nil {
		secretsList := runConfig.Secrets
		registryEnvVars := metadata.EnvVars
		for _, envVar := range registryEnvVars {
			if isEnvVarProvided(envVar.Name, suppliedEnvVars, secretsList) {
				continue
			} else if envVar.Required {
				return nil, fmt.Errorf("missing required environment variable: %s", envVar.Name)
			} else if envVar.Secret {
				return nil, fmt.Errorf("missing required secret environment variable: %s", envVar.Name)
			} else if envVar.Default != "" {
				addAsEnvironmentVariable(envVar, envVar.Default, &suppliedEnvVars)
			}
		}
	}

	return suppliedEnvVars, nil
}

// CLIEnvVarValidator implements the EnvVarValidator interface for
// CLI usage. If any missing, mandatory variables are found, this code will
// prompt the user to supply them through stdin.
type CLIEnvVarValidator struct{}

// Validate checks that all required environment variables and secrets are provided
// and returns the processed environment variables to be set.
func (*CLIEnvVarValidator) Validate(
	ctx context.Context,
	metadata *registry.ImageMetadata,
	runConfig *RunConfig,
	suppliedEnvVars []string,
) ([]string, error) {
	envVars := suppliedEnvVars

	// If we are processing an image from our registry, we need to check the
	// variables defined in the metadata.
	if metadata != nil {
		secretsConfig := runConfig.Secrets
		// Create new slices for extra secrets and environment variables.
		secretsList := make([]string, 0, len(secretsConfig))
		envVars = make([]string, 0, len(suppliedEnvVars))

		// Copy existing env vars and secrets
		envVars = append(envVars, suppliedEnvVars...)
		secretsList = append(secretsList, secretsConfig...)
		registryEnvVars := metadata.EnvVars
		// Create a new slice with capacity for all env vars

		// Initialize secrets manager if needed
		secretsManager := initializeSecretsManagerIfNeeded(registryEnvVars)

		// Process each environment variable from the registry
		for _, envVar := range registryEnvVars {
			if isEnvVarProvided(envVar.Name, envVars, secretsList) {
				continue
			}

			if envVar.Required {
				value, err := promptForEnvironmentVariable(envVar)
				if err != nil {
					logger.Warnf("Warning: Failed to read input for %s: %v", envVar.Name, err)
					continue
				}
				if value != "" {
					addNewVariable(ctx, envVar, value, secretsManager, &envVars, &secretsList)
				}
			} else if envVar.Default != "" {
				addNewVariable(ctx, envVar, envVar.Default, secretsManager, &envVars, &secretsList)
			}
		}

		runConfig.Secrets = secretsList
	}

	return envVars, nil
}

// promptForEnvironmentVariable prompts the user for an environment variable value
func promptForEnvironmentVariable(envVar *registry.EnvVar) (string, error) {
	var byteValue []byte
	var err error
	if envVar.Secret {
		logger.Infof("Required secret environment variable: %s (%s)", envVar.Name, envVar.Description)
		fmt.Printf("Enter value for %s (input will be hidden): ", envVar.Name)
		byteValue, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println() // Move to the next line after hidden input
	} else {
		logger.Infof("Required environment variable: %s (%s)", envVar.Name, envVar.Description)
		fmt.Printf("Enter value for %s: ", envVar.Name)
		// For non-secret input, we can use a simple fmt.Scanln or bufio.Scanner
		var input string
		_, err = fmt.Scanln(&input)
		if err != nil {
			return "", fmt.Errorf("failed to read input for %s: %v", envVar.Name, err)
		}
		byteValue = []byte(input)
	}

	if err != nil {
		return "", fmt.Errorf("failed to read input for %s: %v", envVar.Name, err)
	}

	return strings.TrimSpace(string(byteValue)), nil
}

// addNewVariable adds an environment variable or secret to the appropriate list
func addNewVariable(
	ctx context.Context,
	envVar *registry.EnvVar,
	value string,
	secretsManager secrets.Provider,
	envVars *[]string,
	secretsList *[]string,
) {
	if envVar.Secret && secretsManager != nil {
		addAsSecret(ctx, envVar, value, secretsManager, secretsList, envVars)
	} else {
		addAsEnvironmentVariable(envVar, value, envVars)
	}
}

// addAsSecret stores the value as a secret and adds a secret reference
func addAsSecret(
	ctx context.Context,
	envVar *registry.EnvVar,
	value string,
	secretsManager secrets.Provider,
	secretsList *[]string,
	envVars *[]string,
) {
	var secretName string
	if envVar.Required {
		secretName = fmt.Sprintf("registry-user-%s", strings.ToLower(envVar.Name))
	} else {
		secretName = fmt.Sprintf("registry-default-%s", strings.ToLower(envVar.Name))
	}

	if err := secretsManager.SetSecret(ctx, secretName, value); err != nil {
		logger.Warnf("Warning: Failed to store secret %s: %v", secretName, err)
		logger.Warnf("Falling back to environment variable for %s", envVar.Name)
		*envVars = append(*envVars, fmt.Sprintf("%s=%s", envVar.Name, value))
		logger.Debugf("Added environment variable (secret fallback): %s", envVar.Name)
	} else {
		// Create secret reference for RunConfig
		secretEntry := fmt.Sprintf("%s,target=%s", secretName, envVar.Name)
		*secretsList = append(*secretsList, secretEntry)
		if envVar.Required {
			logger.Debugf("Created secret for %s: %s", envVar.Name, secretName)
		} else {
			logger.Debugf("Created secret with default value for %s: %s", envVar.Name, secretName)
		}
	}
}

// initializeSecretsManagerIfNeeded initializes the secrets manager if there are secret environment variables
func initializeSecretsManagerIfNeeded(registryEnvVars []*registry.EnvVar) secrets.Provider {
	// Check if we have any secret environment variables
	hasSecrets := false
	for _, envVar := range registryEnvVars {
		if envVar.Secret {
			hasSecrets = true
			break
		}
	}

	if !hasSecrets {
		return nil
	}

	secretsManager, err := getSecretsManager()
	if err != nil {
		logger.Warnf("Warning: Failed to initialize secrets manager: %v", err)
		logger.Warnf("Secret environment variables will be stored as regular environment variables")
		return nil
	}

	return secretsManager
}

// Duplicated from cmd/thv/app/app.go
// It may be possible to de-duplicate this in future.
func getSecretsManager() (secrets.Provider, error) {
	cfg := config.GetConfig()

	// Check if secrets setup has been completed
	if !cfg.Secrets.SetupCompleted {
		return nil, secrets.ErrSecretsNotSetup
	}

	providerType, err := cfg.Secrets.GetProviderType()
	if err != nil {
		return nil, fmt.Errorf("failed to get secrets provider type: %w", err)
	}

	manager, err := secrets.CreateSecretProvider(providerType)
	if err != nil {
		return nil, fmt.Errorf("failed to create secrets manager: %w", err)
	}

	return manager, nil
}

// Shared Logic follows

// isEnvVarProvided checks if an environment variable is already provided
func isEnvVarProvided(name string, envVars []string, secretsConfig []string) bool {
	// Check if the environment variable is already provided in the command line
	for _, env := range envVars {
		if strings.HasPrefix(env, name+"=") {
			return true
		}
	}

	// Check if the environment variable is provided as a secret
	return findEnvironmentVariableFromSecrets(secretsConfig, name)
}

func findEnvironmentVariableFromSecrets(secs []string, envVarName string) bool {
	for _, secret := range secs {
		if isSecretReferenceEnvVar(secret, envVarName) {
			return true
		}
	}

	return false
}

func isSecretReferenceEnvVar(secret, envVarName string) bool {
	parts := strings.Split(secret, ",")
	if len(parts) != 2 {
		return false
	}

	targetSplit := strings.Split(parts[1], "=")
	if len(targetSplit) != 2 {
		return false
	}

	if targetSplit[1] == envVarName {
		return true
	}

	return false
}

// addAsEnvironmentVariable adds the value as a regular environment variable
func addAsEnvironmentVariable(
	envVar *registry.EnvVar,
	value string,
	envVars *[]string,
) {
	*envVars = append(*envVars, fmt.Sprintf("%s=%s", envVar.Name, value))

	if envVar.Secret {
		if envVar.Required {
			logger.Debugf("Added secret as environment variable (no secrets manager): %s", envVar.Name)
		} else {
			logger.Debugf("Added default secret as environment variable (no secrets manager): %s", envVar.Name)
		}
	} else {
		if envVar.Required {
			logger.Debugf("Added environment variable: %s", envVar.Name)
		} else {
			logger.Debugf("Using default value for %s: %s", envVar.Name, value)
		}
	}
}

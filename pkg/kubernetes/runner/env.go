package runner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/registry"
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
		// Create new slices for extra secrets and environment variables.
		envVars = make([]string, 0, len(suppliedEnvVars))

		// Copy existing env vars and secrets
		envVars = append(envVars, suppliedEnvVars...)
		registryEnvVars := metadata.EnvVars
		// Create a new slice with capacity for all env vars

		// Process each environment variable from the registry
		for _, envVar := range registryEnvVars {
			if isEnvVarProvided(envVar.Name, envVars) {
				continue
			}

			if envVar.Required {
				value, err := promptForEnvironmentVariable(envVar)
				if err != nil {
					logger.Warnf("Warning: Failed to read input for %s: %v", envVar.Name, err)
					continue
				}
				if value != "" {
					addAsEnvironmentVariable(envVar, value, &envVars)
				}
			} else if envVar.Default != "" {
				addAsEnvironmentVariable(envVar, envVar.Default, &envVars)
			}
		}

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

// isEnvVarProvided checks if an environment variable is already provided
func isEnvVarProvided(name string, envVars []string) bool {
	// Check if the environment variable is already provided in the command line
	for _, env := range envVars {
		if strings.HasPrefix(env, name+"=") {
			return true
		}
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

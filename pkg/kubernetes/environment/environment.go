// Package environment provides utilities for handling environment variables
// and environment-related operations for containers.
package environment

import (
	"fmt"
	"strings"
)

// ParseEnvironmentVariables parses environment variables from a slice of strings
// in the format KEY=VALUE
func ParseEnvironmentVariables(envVars []string) (map[string]string, error) {
	result := make(map[string]string)

	for _, env := range envVars {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid environment variable format: %s", env)
		}

		key := parts[0]
		value := parts[1]

		if key == "" {
			return nil, fmt.Errorf("empty environment variable key")
		}

		result[key] = value
	}

	return result, nil
}

// SetTransportEnvironmentVariables sets transport-specific environment variables
func SetTransportEnvironmentVariables(envVars map[string]string, transportType string, port int) {
	// Set common environment variables
	envVars["MCP_TRANSPORT"] = transportType

	// Set port-related environment variables only if port is greater than 0
	if port > 0 {
		// Set transport-specific environment variables
		switch transportType {
		case "sse":
			envVars["MCP_PORT"] = fmt.Sprintf("%d", port)
			envVars["FASTMCP_PORT"] = fmt.Sprintf("%d", port)
		case "stdio":
			// No additional environment variables needed for stdio transport
		}
	}
}

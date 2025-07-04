package runner

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/permissions"
)

// This was moved from the CLI to allow it to be shared with the lifecycle manager.
// It will likely be moved elsewhere in a future PR.

// CreatePermissionProfileFile creates a temporary file with the permission profile
func CreatePermissionProfileFile(serverName string, permProfile *permissions.Profile) (string, error) {
	tempFile, err := os.CreateTemp("", fmt.Sprintf("toolhive-%s-permissions-*.json", serverName))
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer tempFile.Close()

	// Get the temporary file path
	permProfilePath := tempFile.Name()

	// Serialize the permission profile to JSON
	permProfileJSON, err := json.Marshal(permProfile)
	if err != nil {
		return "", fmt.Errorf("failed to serialize permission profile: %v", err)
	}

	// Write the permission profile to the temporary file
	if _, err := tempFile.Write(permProfileJSON); err != nil {
		return "", fmt.Errorf("failed to write permission profile to file: %v", err)
	}

	logger.Debugf("Wrote permission profile to temporary file: %s", permProfilePath)

	return permProfilePath, nil
}

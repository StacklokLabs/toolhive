// Package updates contains logic for checking if an update is available for ToolHive.
package updates

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/google/uuid"
	"golang.org/x/mod/semver"

	"github.com/stacklok/toolhive/pkg/kubernetes/versions"
)

// UpdateChecker is an interface for checking if a new version of ToolHive is available.
type UpdateChecker interface {
	// CheckLatestVersion checks if a new version of ToolHive is available
	// and prints the result to the console.
	CheckLatestVersion() error
}

// NewUpdateChecker creates a new instance of UpdateChecker.
func NewUpdateChecker(versionClient VersionClient) (UpdateChecker, error) {
	path, err := xdg.DataFile(updateFilePathSuffix)
	if err != nil {
		return nil, fmt.Errorf("unable to access update file path %w", err)
	}

	// Check to see if the file already exists. Read the instance ID from the
	// file if it does. If it doesn't exist, create a new instance ID.
	var instanceID, previousVersion string
	// #nosec G304: File path is not configurable at this time.
	rawContents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			instanceID = uuid.NewString()
		} else {
			return nil, fmt.Errorf("failed to read update file: %w", err)
		}
	} else {
		var contents updateFile
		err = json.Unmarshal(rawContents, &contents)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize update file: %w", err)
		}
		instanceID = contents.InstanceID
		previousVersion = contents.LatestVersion
	}

	return &defaultUpdateChecker{
		currentVersion:      versions.GetVersionInfo().Version,
		instanceID:          instanceID,
		updateFilePath:      path,
		versionClient:       versionClient,
		previousAPIResponse: previousVersion,
	}, nil
}

const (
	updateFilePathSuffix = "toolhive/updates.json"
	updateInterval       = 4 * time.Hour
)

// updateFile represents the structure of the update file.
type updateFile struct {
	InstanceID    string `json:"instance_id"`
	LatestVersion string `json:"latest_version"`
}

type defaultUpdateChecker struct {
	instanceID          string
	currentVersion      string
	previousAPIResponse string
	updateFilePath      string
	versionClient       VersionClient
}

func (d *defaultUpdateChecker) CheckLatestVersion() error {
	// Check if the update file exists.
	// Ignore the error if the file doesn't exist - we'll create it later.
	fileInfo, err := os.Stat(d.updateFilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat update file: %w", err)
	}

	// Check if we need to make an API request based on file modification time.
	if fileInfo != nil && time.Since(fileInfo.ModTime()) < updateInterval {
		// If it is too soon - notify the user if we already know there is
		// an update, then exit.
		notifyIfUpdateAvailable(d.currentVersion, d.previousAPIResponse)
		return nil
	}

	// If the update file is stale or does not exist - get the latest version
	// from the API.
	latestVersion, err := d.versionClient.GetLatestVersion(d.instanceID, d.currentVersion)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	notifyIfUpdateAvailable(d.currentVersion, latestVersion)

	// Rewrite the update file with the latest result.
	// If we really wanted to optimize this, we could skip updating the file
	// if the version is the same as the current version, but update the
	// modification time.
	newFileContents := updateFile{
		InstanceID:    d.instanceID,
		LatestVersion: latestVersion,
	}

	updatedData, err := json.Marshal(newFileContents)
	if err != nil {
		return fmt.Errorf("failed to marshal updated data: %w", err)
	}

	if err := os.WriteFile(d.updateFilePath, updatedData, 0600); err != nil {
		return fmt.Errorf("failed to write updated file: %w", err)
	}

	return nil
}

func notifyIfUpdateAvailable(current, latest string) {
	// Print a meaningful message for people running local builds.
	if strings.HasPrefix(current, "build-") {
		// No need to compare versions, user is already aware they are not on the latest release.
		return
	}
	// Ensure both versions have the 'v' prefix for proper semantic version comparison
	if !semver.IsValid(current) {
		current = fmt.Sprintf("v%s", current)
	}
	if !semver.IsValid(latest) {
		latest = fmt.Sprintf("v%s", latest)
	}
	// Compare the versions ensuring their canonical forms
	if semver.Compare(semver.Canonical(current), semver.Canonical(latest)) < 0 {
		fmt.Fprintf(os.Stderr, "A new version of ToolHive is available: %s\nCurrently running: %s\n", latest, current)
	}
}

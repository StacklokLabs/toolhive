package updates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Constants for testing
const (
	testInstanceID     = "test-instance-id"
	testCurrentVersion = "1.0.0"
	testLatestVersion  = "1.1.0"
	testOldVersion     = "1.0.5"
)

// MockVersionClient is a mock implementation of the VersionClient interface
type MockVersionClient struct {
	mock.Mock
}

func (m *MockVersionClient) GetLatestVersion(instanceID string, currentVersion string) (string, error) {
	args := m.Called(instanceID, currentVersion)
	return args.String(0), args.Error(1)
}

// Test helpers
func setupMockVersionClient(_ *testing.T) *MockVersionClient {
	return &MockVersionClient{}
}

// createTempDir creates a temporary directory for testing
func createTempDir(t *testing.T) string {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "toolhive-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})
	return tempDir
}

// createUpdateFile creates a temporary update file with the given contents
func createUpdateFile(t *testing.T, dir string, contents updateFile) string {
	t.Helper()
	filePath := filepath.Join(dir, "updates.json")
	data, err := json.Marshal(contents)
	require.NoError(t, err)
	err = os.WriteFile(filePath, data, 0600)
	require.NoError(t, err)
	return filePath
}

// TestCheckLatestVersion tests the CheckLatestVersion method
func TestCheckLatestVersion(t *testing.T) {
	t.Parallel()
	t.Run("file doesn't exist - creates new file", func(t *testing.T) {
		t.Parallel()
		// Setup
		mockClient := setupMockVersionClient(t)
		tempDir := createTempDir(t)
		updateFilePath := filepath.Join(tempDir, "updates.json")
		instanceID := testInstanceID
		currentVersion := testCurrentVersion
		latestVersion := testLatestVersion

		// Create the checker directly
		checker := &defaultUpdateChecker{
			instanceID:     instanceID,
			currentVersion: currentVersion,
			updateFilePath: updateFilePath,
			versionClient:  mockClient,
		}

		// Mock client.GetLatestVersion
		mockClient.On("GetLatestVersion", instanceID, currentVersion).Return(latestVersion, nil)

		// Execute
		err := checker.CheckLatestVersion()

		// Verify
		require.NoError(t, err)
		mockClient.AssertExpectations(t)

		// Verify that the file was created
		_, err = os.Stat(updateFilePath)
		assert.NoError(t, err)

		// Read the file and verify its contents
		data, err := os.ReadFile(updateFilePath)
		require.NoError(t, err)

		var fileContents updateFile
		err = json.Unmarshal(data, &fileContents)
		require.NoError(t, err)
		assert.Equal(t, instanceID, fileContents.InstanceID)
		assert.Equal(t, latestVersion, fileContents.LatestVersion)
	})

	t.Run("file exists but is stale - makes API call", func(t *testing.T) {
		t.Parallel()
		// Setup
		mockClient := setupMockVersionClient(t)
		tempDir := createTempDir(t)
		instanceID := testInstanceID
		currentVersion := testCurrentVersion
		oldLatestVersion := testOldVersion
		newLatestVersion := testLatestVersion

		// Create an update file with a known instance ID
		updateFilePath := createUpdateFile(t, tempDir, updateFile{
			InstanceID:    instanceID,
			LatestVersion: oldLatestVersion,
		})

		// Set the file's modification time to be older than updateInterval
		staleTime := time.Now().Add(-5 * time.Hour)
		err := os.Chtimes(updateFilePath, staleTime, staleTime)
		require.NoError(t, err)

		// Create the checker directly
		checker := &defaultUpdateChecker{
			instanceID:     instanceID,
			currentVersion: currentVersion,
			updateFilePath: updateFilePath,
			versionClient:  mockClient,
		}

		// Mock client.GetLatestVersion - must be set up before calling CheckLatestVersion
		mockClient.On("GetLatestVersion", instanceID, currentVersion).Return(newLatestVersion, nil)

		// Execute
		err = checker.CheckLatestVersion()

		// Verify
		require.NoError(t, err)
		mockClient.AssertExpectations(t)

		// Read the file and verify its contents
		data, err := os.ReadFile(updateFilePath)
		require.NoError(t, err)

		var fileContents updateFile
		err = json.Unmarshal(data, &fileContents)
		require.NoError(t, err)
		assert.Equal(t, instanceID, fileContents.InstanceID)
		assert.Equal(t, newLatestVersion, fileContents.LatestVersion)
	})

	t.Run("file exists and is fresh - skips API call", func(t *testing.T) {
		t.Parallel()
		// Setup
		mockClient := setupMockVersionClient(t)
		tempDir := createTempDir(t)
		instanceID := testInstanceID
		currentVersion := testCurrentVersion
		latestVersion := testLatestVersion

		// Create an update file with a known instance ID
		updateFilePath := createUpdateFile(t, tempDir, updateFile{
			InstanceID:    instanceID,
			LatestVersion: latestVersion,
		})

		// Set the file's modification time to be newer than updateInterval (less than 4 hours ago)
		freshTime := time.Now().Add(-1 * time.Hour)
		err := os.Chtimes(updateFilePath, freshTime, freshTime)
		require.NoError(t, err)

		// Create the checker directly
		checker := &defaultUpdateChecker{
			instanceID:          instanceID,
			currentVersion:      currentVersion,
			previousAPIResponse: latestVersion, // Set the previous API response
			updateFilePath:      updateFilePath,
			versionClient:       mockClient,
		}

		// Execute
		err = checker.CheckLatestVersion()

		// Verify
		require.NoError(t, err)

		// Verify that GetLatestVersion was not called
		mockClient.AssertNotCalled(t, "GetLatestVersion")

		// Verify the file wasn't modified
		fileInfo, err := os.Stat(updateFilePath)
		require.NoError(t, err)
		assert.True(t, fileInfo.ModTime().Equal(freshTime), "File modification time should not have changed")
	})

	t.Run("error when stat fails", func(t *testing.T) {
		t.Parallel()
		// Setup
		mockClient := setupMockVersionClient(t)
		tempDir := createTempDir(t)

		// Use a non-existent directory to cause a stat error
		nonExistentDir := filepath.Join(tempDir, "non-existent")
		updateFilePath := filepath.Join(nonExistentDir, "updates.json")

		// Create the checker directly
		checker := &defaultUpdateChecker{
			instanceID:     testInstanceID,
			currentVersion: testCurrentVersion,
			updateFilePath: updateFilePath,
			versionClient:  mockClient,
		}

		// Make the directory read-only to cause a stat error
		err := os.Chmod(tempDir, 0000)
		require.NoError(t, err)

		// Ensure we restore permissions after the test
		defer os.Chmod(tempDir, 0700)

		// Execute
		err = checker.CheckLatestVersion()

		// Verify
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to stat update file")
	})

	t.Run("error when GetLatestVersion fails", func(t *testing.T) {
		t.Parallel()
		// Setup
		mockClient := setupMockVersionClient(t)
		tempDir := createTempDir(t)
		updateFilePath := filepath.Join(tempDir, "updates.json")
		instanceID := testInstanceID
		currentVersion := testCurrentVersion
		expectedError := errors.New("API error")

		// Create the checker directly
		checker := &defaultUpdateChecker{
			instanceID:     instanceID,
			currentVersion: currentVersion,
			updateFilePath: updateFilePath,
			versionClient:  mockClient,
		}

		// Mock client.GetLatestVersion to return an error
		mockClient.On("GetLatestVersion", instanceID, currentVersion).Return("", expectedError)

		// Execute
		err := checker.CheckLatestVersion()

		// Verify
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check for updates")
		mockClient.AssertExpectations(t)
	})
}

// TestNotifyIfUpdateAvailable tests the notifyIfUpdateAvailable function
func TestNotifyIfUpdateAvailable(t *testing.T) {
	t.Parallel()
	t.Run("no update available", func(t *testing.T) {
		t.Parallel()
		// This test is a bit tricky since the function prints to stdout
		// For simplicity, we'll just call it and make sure it doesn't panic
		currentVersion := testCurrentVersion
		latestVersion := testCurrentVersion

		// This shouldn't panic
		notifyIfUpdateAvailable(currentVersion, latestVersion)
	})

	t.Run("update available", func(t *testing.T) {
		t.Parallel()
		// This test is a bit tricky since the function prints to stdout
		// For simplicity, we'll just call it and make sure it doesn't panic
		currentVersion := testCurrentVersion
		latestVersion := testLatestVersion

		// This shouldn't panic
		notifyIfUpdateAvailable(currentVersion, latestVersion)
	})
}

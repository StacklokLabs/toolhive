package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

func TestIsTempPermissionProfile(t *testing.T) {
	t.Parallel()
	logger.Initialize()
	tests := []struct {
		name     string
		filePath string
		expected bool
	}{
		{
			name:     "valid temp file in temp dir",
			filePath: filepath.Join(os.TempDir(), "toolhive-fetch-permissions-123.json"),
			expected: true,
		},
		{
			name:     "valid temp file with different server name",
			filePath: filepath.Join(os.TempDir(), "toolhive-github-permissions-456.json"),
			expected: true,
		},
		{
			name:     "not a toolhive file",
			filePath: filepath.Join(os.TempDir(), "other-file.json"),
			expected: false,
		},
		{
			name:     "toolhive file but not permissions",
			filePath: filepath.Join(os.TempDir(), "toolhive-fetch-config-123.json"),
			expected: false,
		},
		{
			name:     "toolhive permissions file but not in temp dir",
			filePath: "/home/user/toolhive-fetch-permissions-123.json",
			expected: false,
		},
		{
			name:     "empty path",
			filePath: "",
			expected: false,
		},
		{
			name:     "not json file",
			filePath: filepath.Join(os.TempDir(), "toolhive-fetch-permissions-123.txt"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isTempPermissionProfile(tt.filePath)
			if result != tt.expected {
				t.Errorf("isTempPermissionProfile(%q) = %v, expected %v", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestCleanupTempPermissionProfile(t *testing.T) {
	t.Parallel()
	logger.Initialize()
	// Create a temporary file that matches our pattern
	tempFile, err := os.CreateTemp("", "toolhive-test-permissions-*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close()

	// Verify the file exists
	if _, err := os.Stat(tempPath); os.IsNotExist(err) {
		t.Fatalf("Temp file should exist: %s", tempPath)
	}

	// Clean up the temp file
	err = CleanupTempPermissionProfile(tempPath)
	if err != nil {
		t.Fatalf("CleanupTempPermissionProfile failed: %v", err)
	}

	// Verify the file is removed
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("Temp file should be removed: %s", tempPath)
	}
}

func TestCleanupTempPermissionProfile_NonTempFile(t *testing.T) {
	t.Parallel()
	logger.Initialize()
	// Test with a non-temp file path
	nonTempPath := "/home/user/my-permissions.json"

	// This should not fail and should not attempt to remove the file
	err := CleanupTempPermissionProfile(nonTempPath)
	if err != nil {
		t.Errorf("CleanupTempPermissionProfile should not fail for non-temp files: %v", err)
	}
}

func TestCleanupTempPermissionProfile_NonExistentFile(t *testing.T) {
	t.Parallel()
	logger.Initialize()
	// Test with a temp file pattern that doesn't exist
	nonExistentPath := filepath.Join(os.TempDir(), "toolhive-nonexistent-permissions-999.json")

	// This should not fail
	err := CleanupTempPermissionProfile(nonExistentPath)
	if err != nil {
		t.Errorf("CleanupTempPermissionProfile should not fail for non-existent files: %v", err)
	}
}

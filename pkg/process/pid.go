// Package process provides utilities for managing process-related operations,
// such as PID file handling and process management.
package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// GetPIDFilePath returns the path to the PID file for a container
func GetPIDFilePath(containerBaseName string) string {
	// Use the system temporary directory
	tmpDir := os.TempDir()
	return filepath.Join(tmpDir, fmt.Sprintf("toolhive-%s.pid", containerBaseName))
}

// WritePIDFile writes a process ID to a file
func WritePIDFile(containerBaseName string, pid int) error {
	// Get the PID file path
	pidFilePath := GetPIDFilePath(containerBaseName)

	// Write the PID to the file
	return os.WriteFile(pidFilePath, []byte(fmt.Sprintf("%d", pid)), 0600)
}

// WriteCurrentPIDFile writes the current process ID to a file
func WriteCurrentPIDFile(containerBaseName string) error {
	return WritePIDFile(containerBaseName, os.Getpid())
}

// ReadPIDFile reads the process ID from a file
func ReadPIDFile(containerBaseName string) (int, error) {
	// Get the PID file path
	pidFilePath := GetPIDFilePath(containerBaseName)

	// Read the PID from the file
	// #nosec G304 - This is safe as the path is constructed from a known prefix and container name
	pidBytes, err := os.ReadFile(pidFilePath)
	if err != nil {
		return 0, fmt.Errorf("failed to read PID file: %w", err)
	}

	// Parse the PID
	pidStr := strings.TrimSpace(string(pidBytes))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %w", err)
	}

	return pid, nil
}

// RemovePIDFile removes the PID file
func RemovePIDFile(containerBaseName string) error {
	// Get the PID file path
	pidFilePath := GetPIDFilePath(containerBaseName)

	// Remove the file
	return os.Remove(pidFilePath)
}

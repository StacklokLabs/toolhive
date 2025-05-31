//go:build !windows
// +build !windows

package process

import (
	"fmt"
	"os"
	"syscall"
)

// FindProcess finds a process by its ID and checks if it's running.
// This function works on Unix systems (Linux and macOS).
func FindProcess(pid int) (bool, error) {
	// On Unix systems, os.FindProcess always succeeds regardless of whether
	// the process exists or not. We need to send a signal to check if it's running.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("failed to find process: %w", err)
	}

	// Send signal 0 to check if the process exists
	// Signal 0 doesn't actually send a signal, but it checks if the process exists
	err = proc.Signal(syscall.Signal(0))

	// If there's no error, the process exists
	if err == nil {
		return true, nil
	}

	// If the error is "no such process", the process doesn't exist
	if err.Error() == "no such process" || err.Error() == "os: process already finished" {
		return false, nil
	}

	// For other errors (e.g., permission denied), return the error
	return false, fmt.Errorf("error checking process: %w", err)
}

//go:build windows
// +build windows

package process

import (
	"fmt"
	"os"
)

// KillProcess kills a process by its ID on Windows
func KillProcess(pid int) error {
	// Check if the process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// On Windows, os.Process.Kill() calls TerminateProcess with exit code 1
	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to terminate process: %w", err)
	}

	return nil
}

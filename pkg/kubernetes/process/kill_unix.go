//go:build !windows
// +build !windows

package process

import (
	"fmt"
	"os"
	"syscall"
)

// KillProcess kills a process by its ID
func KillProcess(pid int) error {
	// Check if the process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// Send a SIGTERM signal to the process
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process: %w", err)
	}

	return nil
}

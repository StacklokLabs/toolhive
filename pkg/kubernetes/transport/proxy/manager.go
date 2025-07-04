// Package proxy contains code for managing proxy processes.
package proxy

import (
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/process"
)

// We may want to move these operations behind an interface. For now, they
// have been moved to this package to keep proxy-related logic grouped together.

// StopProcess stops the proxy process associated with the container
func StopProcess(containerBaseName string) {
	if containerBaseName == "" {
		logger.Warnf("Warning: Could not find base container name in labels")
		return
	}

	// Try to read the PID file and kill the process
	pid, err := process.ReadPIDFile(containerBaseName)
	if err != nil {
		logger.Errorf("No PID file found for %s, proxy may not be running in detached mode", containerBaseName)
		return
	}

	// PID file found, try to kill the process
	logger.Infof("Stopping proxy process (PID: %d)...", pid)
	if err := process.KillProcess(pid); err != nil {
		logger.Warnf("Warning: Failed to kill proxy process: %v", err)
	} else {
		logger.Info("Proxy process stopped")
	}

	// Remove the PID file
	if err := process.RemovePIDFile(containerBaseName); err != nil {
		logger.Warnf("Warning: Failed to remove PID file: %v", err)
	}
}

// IsRunning checks if the proxy process is running
func IsRunning(containerBaseName string) bool {
	if containerBaseName == "" {
		return false
	}

	// Try to read the PID file
	pid, err := process.ReadPIDFile(containerBaseName)
	if err != nil {
		return false
	}

	// Check if the process exists and is running
	isRunning, err := process.FindProcess(pid)
	if err != nil {
		logger.Warnf("Warning: Error checking process: %v", err)
		return false
	}

	return isRunning
}

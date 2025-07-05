//go:build !windows
// +build !windows

package workloads

import (
	"syscall"
)

// getSysProcAttr returns the platform-specific SysProcAttr for detaching processes
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true, // Create a new session (Unix only)
	}
}

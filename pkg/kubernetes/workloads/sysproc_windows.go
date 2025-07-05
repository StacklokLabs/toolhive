//go:build windows
// +build windows

package workloads

import (
	"syscall"
)

// getSysProcAttr returns the platform-specific SysProcAttr for detaching processes
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		// Windows doesn't have Setsid
		// Instead, use CreationFlags with CREATE_NEW_PROCESS_GROUP and DETACHED_PROCESS
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // 0x00000008 is DETACHED_PROCESS
	}
}

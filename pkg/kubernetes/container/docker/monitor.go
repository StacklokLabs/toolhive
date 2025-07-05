package docker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
)

// ContainerMonitor watches a container's state and reports when it exits
type ContainerMonitor struct {
	runtime       runtime.Runtime
	containerID   string
	containerName string
	stopCh        chan struct{}
	errorCh       chan error
	wg            sync.WaitGroup
	running       bool
	mutex         sync.Mutex
}

// NewMonitor creates a new container monitor
func NewMonitor(rt runtime.Runtime, containerID, containerName string) runtime.Monitor {
	return &ContainerMonitor{
		runtime:       rt,
		containerID:   containerID,
		containerName: containerName,
		stopCh:        make(chan struct{}),
		errorCh:       make(chan error, 1), // Buffered to prevent blocking
	}
}

// StartMonitoring starts monitoring the container
func (m *ContainerMonitor) StartMonitoring(ctx context.Context) (<-chan error, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.running {
		return m.errorCh, nil // Already monitoring
	}

	// Check if the workload exists and is running
	running, err := m.runtime.IsWorkloadRunning(ctx, m.containerID)
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, NewContainerError(ErrContainerNotRunning, m.containerID, "container is not running")
	}

	m.running = true
	m.wg.Add(1)

	// Start monitoring in a goroutine
	go m.monitor(ctx)

	return m.errorCh, nil
}

// StopMonitoring stops monitoring the container
func (m *ContainerMonitor) StopMonitoring() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.running {
		return // Not monitoring
	}

	close(m.stopCh)
	m.wg.Wait()
	m.running = false
}

// monitor checks the container status periodically
func (m *ContainerMonitor) monitor(ctx context.Context) {
	defer m.wg.Done()

	// Check interval
	checkInterval := 5 * time.Second

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context canceled
			return
		case <-m.stopCh:
			// Monitoring stopped
			return
		case <-ticker.C:
			// Check if the container is still running
			// Create a short timeout context for this check
			checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			running, err := m.runtime.IsWorkloadRunning(checkCtx, m.containerID)
			cancel() // Always cancel the context to avoid leaks
			if err != nil {
				// If the container is not found, it may have been removed
				if IsContainerNotFound(err) {
					exitErr := NewContainerError(
						ErrContainerExited,
						m.containerID,
						fmt.Sprintf("Container %s (%s) not found, it may have been removed", m.containerName, m.containerID),
					)
					m.errorCh <- exitErr
					return
				}

				// For other errors, log and continue
				continue
			}

			if !running {
				// Container has exited, get logs and info
				// Create a short timeout context for these operations
				infoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				logs, _ := m.runtime.GetWorkloadLogs(infoCtx, m.containerID, false)
				info, _ := m.runtime.GetWorkloadInfo(infoCtx, m.containerID)
				cancel() // Always cancel the context to avoid leaks

				exitErr := NewContainerError(
					ErrContainerExited,
					m.containerID,
					fmt.Sprintf("Container %s (%s) exited unexpectedly. Status: %s. Last logs:\n%s",
						m.containerName, m.containerID, info.Status, logs),
				)
				m.errorCh <- exitErr
				return
			}
		}
	}
}

// IsContainerNotFound checks if the error is a container not found error
func IsContainerNotFound(err error) bool {
	return err == ErrContainerNotFound || (err != nil && err.Error() == ErrContainerNotFound.Error())
}

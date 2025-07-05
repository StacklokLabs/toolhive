//go:build windows
// +build windows

package sdk

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/docker/docker/client"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

var ErrRuntimeNotFound = fmt.Errorf("container runtime not found")

// Windows named pipe paths
const (
	// DockerDesktopWindowsPipePath is the Docker Desktop named pipe path on Windows
	DockerDesktopWindowsPipePath = `\\.\pipe\docker_engine`

	// PodmanDesktopWindowsPipePath is the Podman Desktop named pipe path on Windows
	PodmanDesktopWindowsPipePath = `\\.\pipe\podman-api`
)

// Windows named pipe connection timeout
const pipeConnectionTimeout = 2 * time.Second

// newPlatformClient creates a Docker client using Windows named pipes
func newPlatformClient(pipePath string) (*http.Client, []client.Opt) {
	// Create a custom HTTP client that uses Windows named pipes
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				// Create a context with timeout for the pipe connection
				dialCtx, cancel := context.WithTimeout(ctx, pipeConnectionTimeout)
				defer cancel()
				return winio.DialPipeContext(dialCtx, pipePath)
			},
		},
	}

	// Create Docker client options
	opts := []client.Opt{
		client.WithAPIVersionNegotiation(),
		client.WithHTTPClient(httpClient),
		client.WithHost("npipe://" + pipePath),
	}

	return httpClient, opts
}

// findPlatformContainerSocket finds a container socket path on Windows
func findPlatformContainerSocket(rt runtime.Type) (string, runtime.Type, error) {
	// First check for custom socket paths via environment variables
	if customPipePath := os.Getenv(PodmanSocketEnv); customPipePath != "" {
		logger.Debugf("Using Podman pipe from env: %s", customPipePath)
		// Validate the pipe path exists with timeout
		ctx, cancel := context.WithTimeout(context.Background(), pipeConnectionTimeout)
		defer cancel()
		conn, err := winio.DialPipeContext(ctx, customPipePath)
		if err != nil {
			return "", runtime.TypePodman, fmt.Errorf("invalid Podman pipe path: %w", err)
		}
		conn.Close()
		return customPipePath, runtime.TypePodman, nil
	}

	if customPipePath := os.Getenv(DockerSocketEnv); customPipePath != "" {
		logger.Debugf("Using Docker pipe from env: %s", customPipePath)
		// Validate the pipe path exists with timeout
		ctx, cancel := context.WithTimeout(context.Background(), pipeConnectionTimeout)
		defer cancel()
		conn, err := winio.DialPipeContext(ctx, customPipePath)
		if err != nil {
			return "", runtime.TypeDocker, fmt.Errorf("invalid Docker pipe path: %w", err)
		}
		conn.Close()
		return customPipePath, runtime.TypeDocker, nil
	}

	if rt == runtime.TypePodman {
		// Try Podman named pipe with timeout
		ctx, cancel := context.WithTimeout(context.Background(), pipeConnectionTimeout)
		defer cancel()
		conn, err := winio.DialPipeContext(ctx, PodmanDesktopWindowsPipePath)
		if err == nil {
			logger.Debugf("Found Podman pipe at %s", PodmanDesktopWindowsPipePath)
			conn.Close()
			return PodmanDesktopWindowsPipePath, runtime.TypePodman, nil
		}
		logger.Debugf("Failed to connect to Podman pipe at %s: %v", PodmanDesktopWindowsPipePath, err)
	}

	if rt == runtime.TypeDocker {
		// Try Docker named pipe with timeout
		ctx, cancel := context.WithTimeout(context.Background(), pipeConnectionTimeout)
		defer cancel()
		conn, err := winio.DialPipeContext(ctx, DockerDesktopWindowsPipePath)
		if err == nil {
			logger.Debugf("Found Docker pipe at %s", DockerDesktopWindowsPipePath)
			conn.Close()
			return DockerDesktopWindowsPipePath, runtime.TypeDocker, nil
		}
		logger.Debugf("Failed to connect to Docker pipe at %s: %v", DockerDesktopWindowsPipePath, err)
	}

	return "", "", ErrRuntimeNotFound
}

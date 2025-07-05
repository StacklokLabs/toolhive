// Package sdk provides a factory method for creating a Docker client.
package sdk

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"

	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

/*
 * This file contains logic for instantiating the Docker SDK client.
 * In future, this may be centralized behind a single client wrapper.
 */

// Environment variable names
const (
	// DockerSocketEnv is the environment variable for custom Docker socket path
	DockerSocketEnv = "TOOLHIVE_DOCKER_SOCKET"
	// PodmanSocketEnv is the environment variable for custom Podman socket path
	PodmanSocketEnv = "TOOLHIVE_PODMAN_SOCKET"
)

// Common socket paths
const (
	// PodmanSocketPath is the default Podman socket path
	PodmanSocketPath = "/var/run/podman/podman.sock"
	// PodmanXDGRuntimeSocketPath is the XDG runtime Podman socket path
	PodmanXDGRuntimeSocketPath = "podman/podman.sock"
	// DockerSocketPath is the default Docker socket path
	DockerSocketPath = "/var/run/docker.sock"
	// DockerDesktopMacSocketPath is the Docker Desktop socket path on macOS
	DockerDesktopMacSocketPath = ".docker/run/docker.sock"
)

var supportedSocketPaths = []runtime.Type{runtime.TypePodman, runtime.TypeDocker}

// NewDockerClient creates a new container client
func NewDockerClient(ctx context.Context) (*client.Client, string, runtime.Type, error) {
	var lastErr error

	// We try to find a container socket for the given runtime
	// We try Podman first, then Docker as fallback
	for _, sp := range supportedSocketPaths {
		// Try to find a container socket for the given runtime
		socketPath, runtimeType, err := findContainerSocket(sp)
		if err != nil {
			logger.Debugf("Failed to find socket for %s: %v", sp, err)
			lastErr = err
			continue
		}

		c, err := newClientWithSocketPath(ctx, socketPath)
		if err != nil {
			lastErr = err
			logger.Debugf("Failed to create client for %s: %v", sp, err)
			continue
		}

		logger.Debugf("Successfully connected to %s runtime", runtimeType)
		return c, socketPath, runtimeType, nil
	}

	if lastErr != nil {
		return nil, "", "", fmt.Errorf("no supported container runtime available: %w", lastErr)
	}
	return nil, "", "", fmt.Errorf("no supported container runtime found/running")
}

// NewClientWithSocketPath creates a new container client with a specific socket path
func newClientWithSocketPath(ctx context.Context, socketPath string) (*client.Client, error) {
	// Create platform-specific client
	_, opts := newPlatformClient(socketPath)

	// Create Docker client with the custom HTTP client
	dockerClient, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Make sure we can ping the server.
	_, err = dockerClient.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ping Docker server at %s: %w", socketPath, err)
	}

	return dockerClient, nil
}

// findContainerSocket finds a container socket path, preferring Podman over Docker
func findContainerSocket(rt runtime.Type) (string, runtime.Type, error) {
	// Use platform-specific implementation
	return findPlatformContainerSocket(rt)
}

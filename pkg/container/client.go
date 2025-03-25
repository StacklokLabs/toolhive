// Package container provides utilities for managing containers,
// including creating, starting, stopping, and monitoring containers.
package container

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/stacklok/vibetool/pkg/permissions"
)

// Common socket paths
const (
	// PodmanSocketPath is the default Podman socket path
	PodmanSocketPath = "/var/run/podman/podman.sock"
	// PodmanXDGRuntimeSocketPath is the XDG runtime Podman socket path
	PodmanXDGRuntimeSocketPath = "podman/podman.sock"
	// DockerSocketPath is the default Docker socket path
	DockerSocketPath = "/var/run/docker.sock"
)

// Client implements the Runtime interface for container operations
type Client struct {
	runtimeType RuntimeType
	socketPath  string
	client      *client.Client
}

// NewClient creates a new container client
func NewClient(ctx context.Context) (*Client, error) {
	// Try to find a container socket in various locations
	socketPath, runtimeType, err := findContainerSocket()
	if err != nil {
		return nil, err
	}

	return NewClientWithSocketPath(ctx, socketPath, runtimeType)
}

// NewClientWithSocketPath creates a new container client with a specific socket path
func NewClientWithSocketPath(ctx context.Context, socketPath string, runtimeType RuntimeType) (*Client, error) {
	// Create a custom HTTP client that uses the Unix socket
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	// Create Docker client with the custom HTTP client
	opts := []client.Opt{
		client.WithAPIVersionNegotiation(),
		client.WithHTTPClient(httpClient),
		client.WithHost("unix://" + socketPath),
	}

	dockerClient, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, NewContainerError(err, "", fmt.Sprintf("failed to create client: %v", err))
	}

	c := &Client{
		runtimeType: runtimeType,
		socketPath:  socketPath,
		client:      dockerClient,
	}

	// Verify that the container runtime is available
	if err := c.ping(ctx); err != nil {
		return nil, err
	}

	return c, nil
}

// ping checks if the container runtime is available
func (c *Client) ping(ctx context.Context) error {
	_, err := c.client.Ping(ctx)
	if err != nil {
		return NewContainerError(ErrRuntimeNotFound, "", fmt.Sprintf("failed to ping %s: %v", c.runtimeType, err))
	}
	return nil
}

// findContainerSocket finds a container socket path, preferring Podman over Docker
func findContainerSocket() (string, RuntimeType, error) {
	// Try Podman sockets first
	// Check standard Podman location
	if _, err := os.Stat(PodmanSocketPath); err == nil {
		return PodmanSocketPath, RuntimeTypePodman, nil
	}

	// Check XDG_RUNTIME_DIR location for Podman
	if xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntimeDir != "" {
		xdgSocketPath := filepath.Join(xdgRuntimeDir, PodmanXDGRuntimeSocketPath)
		if _, err := os.Stat(xdgSocketPath); err == nil {
			return xdgSocketPath, RuntimeTypePodman, nil
		}
	}

	// Check user-specific location for Podman
	if home := os.Getenv("HOME"); home != "" {
		userSocketPath := filepath.Join(home, ".local/share/containers/podman/machine/podman.sock")
		if _, err := os.Stat(userSocketPath); err == nil {
			return userSocketPath, RuntimeTypePodman, nil
		}
	}

	// Try Docker socket as fallback
	if _, err := os.Stat(DockerSocketPath); err == nil {
		return DockerSocketPath, RuntimeTypeDocker, nil
	}

	return "", "", ErrRuntimeNotFound
}

// CreateContainer creates a container without starting it
// If options is nil, default options will be used
// convertEnvVars converts a map of environment variables to a slice
func convertEnvVars(envVars map[string]string) []string {
	env := make([]string, 0, len(envVars))
	for k, v := range envVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

// convertMounts converts internal mount format to Docker mount format
func convertMounts(mounts []Mount) []mount.Mount {
	result := make([]mount.Mount, 0, len(mounts))
	for _, m := range mounts {
		result = append(result, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	return result
}

// setupExposedPorts configures exposed ports for a container
func setupExposedPorts(config *container.Config, exposedPorts map[string]struct{}) error {
	if len(exposedPorts) == 0 {
		return nil
	}

	config.ExposedPorts = nat.PortSet{}
	for port := range exposedPorts {
		natPort, err := nat.NewPort("tcp", strings.Split(port, "/")[0])
		if err != nil {
			return fmt.Errorf("failed to parse port: %v", err)
		}
		config.ExposedPorts[natPort] = struct{}{}
	}

	return nil
}

// setupPortBindings configures port bindings for a container
func setupPortBindings(hostConfig *container.HostConfig, portBindings map[string][]PortBinding) error {
	if len(portBindings) == 0 {
		return nil
	}

	hostConfig.PortBindings = nat.PortMap{}
	for port, bindings := range portBindings {
		natPort, err := nat.NewPort("tcp", strings.Split(port, "/")[0])
		if err != nil {
			return fmt.Errorf("failed to parse port: %v", err)
		}

		natBindings := make([]nat.PortBinding, len(bindings))
		for i, binding := range bindings {
			natBindings[i] = nat.PortBinding{
				HostIP:   binding.HostIP,
				HostPort: binding.HostPort,
			}
		}
		hostConfig.PortBindings[natPort] = natBindings
	}

	return nil
}

// CreateContainer creates a container without starting it.
// It configures the container based on the provided permission profile and transport type.
// If options is nil, default options will be used.
func (c *Client) CreateContainer(
	ctx context.Context,
	image, name string,
	command []string,
	envVars, labels map[string]string,
	permissionProfile *permissions.Profile,
	transportType string,
	options *CreateContainerOptions,
) (string, error) {
	// Get permission config from profile
	permissionConfig, err := c.getPermissionConfigFromProfile(permissionProfile, transportType)
	if err != nil {
		return "", fmt.Errorf("failed to get permission config: %w", err)
	}

	// Determine if we should attach stdio
	attachStdio := options == nil || options.AttachStdio

	// Create container configuration
	config := &container.Config{
		Image:        image,
		Cmd:          command,
		Env:          convertEnvVars(envVars),
		Labels:       labels,
		AttachStdin:  attachStdio,
		AttachStdout: attachStdio,
		AttachStderr: attachStdio,
		OpenStdin:    attachStdio,
		Tty:          false,
	}

	// Create host configuration
	hostConfig := &container.HostConfig{
		Mounts:      convertMounts(permissionConfig.Mounts),
		NetworkMode: container.NetworkMode(permissionConfig.NetworkMode),
		CapAdd:      permissionConfig.CapAdd,
		CapDrop:     permissionConfig.CapDrop,
		SecurityOpt: permissionConfig.SecurityOpt,
	}

	// Configure ports if options are provided
	if options != nil {
		// Setup exposed ports
		if err := setupExposedPorts(config, options.ExposedPorts); err != nil {
			return "", NewContainerError(err, "", err.Error())
		}

		// Setup port bindings
		if err := setupPortBindings(hostConfig, options.PortBindings); err != nil {
			return "", NewContainerError(err, "", err.Error())
		}
	}

	// Create the container
	resp, err := c.client.ContainerCreate(
		ctx,
		config,
		hostConfig,
		&network.NetworkingConfig{},
		nil,
		name,
	)
	if err != nil {
		return "", NewContainerError(err, "", fmt.Sprintf("failed to create container: %v", err))
	}

	return resp.ID, nil
}

// StartContainer starts a container
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	err := c.client.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return NewContainerError(err, containerID, fmt.Sprintf("failed to start container: %v", err))
	}
	return nil
}

// ListContainers lists containers
func (c *Client) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	// Create filter for vibetool containers
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "vibetool=true")

	// List containers
	containers, err := c.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, NewContainerError(err, "", fmt.Sprintf("failed to list containers: %v", err))
	}

	// Convert to our ContainerInfo format
	result := make([]ContainerInfo, 0, len(containers))
	for _, c := range containers {
		// Extract container name (remove leading slash)
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
			name = strings.TrimPrefix(name, "/")
		}

		// Extract port mappings
		ports := make([]PortMapping, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, PortMapping{
				ContainerPort: int(p.PrivatePort),
				HostPort:      int(p.PublicPort),
				Protocol:      p.Type,
			})
		}

		// Convert creation time
		created := time.Unix(c.Created, 0)

		result = append(result, ContainerInfo{
			ID:      c.ID,
			Name:    name,
			Image:   c.Image,
			Status:  c.Status,
			State:   c.State,
			Created: created,
			Labels:  c.Labels,
			Ports:   ports,
		})
	}

	return result, nil
}

// StopContainer stops a container
func (c *Client) StopContainer(ctx context.Context, containerID string) error {
	// Use a reasonable timeout
	timeoutSeconds := 30
	err := c.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeoutSeconds})
	if err != nil {
		return NewContainerError(err, containerID, fmt.Sprintf("failed to stop container: %v", err))
	}
	return nil
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	err := c.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: true,
	})
	if err != nil {
		return NewContainerError(err, containerID, fmt.Sprintf("failed to remove container: %v", err))
	}
	return nil
}

// ContainerLogs gets container logs
func (c *Client) ContainerLogs(ctx context.Context, containerID string) (string, error) {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	}

	// Get logs
	logs, err := c.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return "", NewContainerError(err, containerID, fmt.Sprintf("failed to get container logs: %v", err))
	}
	defer logs.Close()

	// Read logs
	logBytes, err := io.ReadAll(logs)
	if err != nil {
		return "", NewContainerError(err, containerID, fmt.Sprintf("failed to read container logs: %v", err))
	}

	return string(logBytes), nil
}

// IsContainerRunning checks if a container is running
func (c *Client) IsContainerRunning(ctx context.Context, containerID string) (bool, error) {
	// Inspect container
	info, err := c.client.ContainerInspect(ctx, containerID)
	if err != nil {
		// Check if the error is because the container doesn't exist
		if client.IsErrNotFound(err) {
			return false, NewContainerError(ErrContainerNotFound, containerID, "container not found")
		}
		return false, NewContainerError(err, containerID, fmt.Sprintf("failed to inspect container: %v", err))
	}

	return info.State.Running, nil
}

// GetContainerInfo gets container information
func (c *Client) GetContainerInfo(ctx context.Context, containerID string) (ContainerInfo, error) {
	// Inspect container
	info, err := c.client.ContainerInspect(ctx, containerID)
	if err != nil {
		// Check if the error is because the container doesn't exist
		if client.IsErrNotFound(err) {
			return ContainerInfo{}, NewContainerError(ErrContainerNotFound, containerID, "container not found")
		}
		return ContainerInfo{}, NewContainerError(err, containerID, fmt.Sprintf("failed to inspect container: %v", err))
	}

	// Extract port mappings
	ports := make([]PortMapping, 0)
	for containerPort, bindings := range info.NetworkSettings.Ports {
		for _, binding := range bindings {
			hostPort := 0
			if _, err := fmt.Sscanf(binding.HostPort, "%d", &hostPort); err != nil {
				// If we can't parse the port, just use 0
				fmt.Printf("Warning: Failed to parse host port %s: %v\n", binding.HostPort, err)
			}

			ports = append(ports, PortMapping{
				ContainerPort: containerPort.Int(),
				HostPort:      hostPort,
				Protocol:      containerPort.Proto(),
			})
		}
	}

	// Convert creation time
	created, err := time.Parse(time.RFC3339, info.Created)
	if err != nil {
		created = time.Time{} // Use zero time if parsing fails
	}

	return ContainerInfo{
		ID:      info.ID,
		Name:    strings.TrimPrefix(info.Name, "/"),
		Image:   info.Config.Image,
		Status:  info.State.Status,
		State:   info.State.Status,
		Created: created,
		Labels:  info.Config.Labels,
		Ports:   ports,
	}, nil
}

// GetContainerIP gets container IP address
func (c *Client) GetContainerIP(ctx context.Context, containerID string) (string, error) {
	// Inspect container
	info, err := c.client.ContainerInspect(ctx, containerID)
	if err != nil {
		// Check if the error is because the container doesn't exist
		if client.IsErrNotFound(err) {
			return "", NewContainerError(ErrContainerNotFound, containerID, "container not found")
		}
		return "", NewContainerError(err, containerID, fmt.Sprintf("failed to inspect container: %v", err))
	}

	// Get IP address from the default network
	for _, netInfo := range info.NetworkSettings.Networks {
		if netInfo.IPAddress != "" {
			return netInfo.IPAddress, nil
		}
	}

	return "", NewContainerError(fmt.Errorf("no IP address found"), containerID, "container has no IP address")
}

// readCloserWrapper wraps an io.Reader to implement io.ReadCloser
type readCloserWrapper struct {
	reader io.Reader
}

func (r *readCloserWrapper) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func (*readCloserWrapper) Close() error {
	// No-op close for readers that don't need closing
	return nil
}

// AttachContainer attaches to a container
func (c *Client) AttachContainer(ctx context.Context, containerID string) (io.WriteCloser, io.ReadCloser, error) {
	// Check if container exists and is running
	running, err := c.IsContainerRunning(ctx, containerID)
	if err != nil {
		return nil, nil, err
	}
	if !running {
		return nil, nil, NewContainerError(ErrContainerNotRunning, containerID, "container is not running")
	}

	// Attach to container
	resp, err := c.client.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return nil, nil, NewContainerError(ErrAttachFailed, containerID, fmt.Sprintf("failed to attach to container: %v", err))
	}

	// Wrap the reader in a ReadCloser
	readCloser := &readCloserWrapper{reader: resp.Reader}

	return resp.Conn, readCloser, nil
}

// ImageExists checks if an image exists locally
func (c *Client) ImageExists(ctx context.Context, imageName string) (bool, error) {
	// List images with the specified name
	filterArgs := filters.NewArgs()
	filterArgs.Add("reference", imageName)

	images, err := c.client.ImageList(ctx, dockerimage.ListOptions{
		Filters: filterArgs,
	})
	if err != nil {
		return false, NewContainerError(err, "", fmt.Sprintf("failed to list images: %v", err))
	}

	return len(images) > 0, nil
}

// PullImage pulls an image from a registry
func (c *Client) PullImage(ctx context.Context, imageName string) error {
	fmt.Printf("Pulling image: %s\n", imageName)

	// Pull the image
	reader, err := c.client.ImagePull(ctx, imageName, dockerimage.PullOptions{})
	if err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to pull image: %v", err))
	}
	defer reader.Close()

	// Read the output to ensure the pull completes
	_, err = io.Copy(os.Stdout, reader)
	if err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to read pull output: %v", err))
	}

	return nil
}

// getPermissionConfigFromProfile converts a permission profile to a container permission config
// with transport-specific settings (internal function)
// addReadOnlyMounts adds read-only mounts to the permission config
func (*Client) addReadOnlyMounts(config *PermissionConfig, paths []string) {
	for _, path := range paths {
		// Skip relative paths
		if !filepath.IsAbs(path) {
			continue
		}

		config.Mounts = append(config.Mounts, Mount{
			Source:   path,
			Target:   path,
			ReadOnly: true,
		})
	}
}

// addReadWriteMounts adds read-write mounts to the permission config
func (*Client) addReadWriteMounts(config *PermissionConfig, paths []string) {
	for _, path := range paths {
		// Skip relative paths
		if !filepath.IsAbs(path) {
			continue
		}

		// Check if the path is already mounted read-only
		alreadyMounted := false
		for i, m := range config.Mounts {
			if m.Target == path {
				// Update the mount to be read-write
				config.Mounts[i].ReadOnly = false
				alreadyMounted = true
				break
			}
		}

		// If not already mounted, add a new mount
		if !alreadyMounted {
			config.Mounts = append(config.Mounts, Mount{
				Source:   path,
				Target:   path,
				ReadOnly: false,
			})
		}
	}
}

// needsNetworkAccess determines if the container needs network access
func (*Client) needsNetworkAccess(profile *permissions.Profile, transportType string) bool {
	// SSE transport always needs network access
	if transportType == "sse" {
		return true
	}

	// Check if the profile has network settings that require network access
	if profile.Network != nil && profile.Network.Outbound != nil {
		outbound := profile.Network.Outbound

		// Any of these conditions require network access
		if outbound.InsecureAllowAll ||
			len(outbound.AllowTransport) > 0 ||
			len(outbound.AllowHost) > 0 ||
			len(outbound.AllowPort) > 0 {
			return true
		}
	}

	return false
}

func (c *Client) getPermissionConfigFromProfile(profile *permissions.Profile, transportType string) (*PermissionConfig, error) {
	// Start with a default permission config
	config := &PermissionConfig{
		Mounts:      []Mount{},
		NetworkMode: "none",
		CapDrop:     []string{"ALL"},
		CapAdd:      []string{},
		SecurityOpt: []string{},
	}

	// Add mounts
	c.addReadOnlyMounts(config, profile.Read)
	c.addReadWriteMounts(config, profile.Write)

	// Determine network mode
	if c.needsNetworkAccess(profile, transportType) {
		config.NetworkMode = "bridge"
	}

	// Validate transport type
	if transportType != "sse" && transportType != "stdio" {
		return nil, fmt.Errorf("unsupported transport type: %s", transportType)
	}

	return config, nil
}

// Package docker provides Docker-specific implementation of container runtime,
// including creating, starting, stopping, and monitoring containers.
package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/stacklok/toolhive/pkg/container/runtime"
	"github.com/stacklok/toolhive/pkg/container/verifier"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/permissions"
	"github.com/stacklok/toolhive/pkg/registry"
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

// Environment variable names
const (
	// DockerSocketEnv is the environment variable for custom Docker socket path
	DockerSocketEnv = "TOOLHIVE_DOCKER_SOCKET"
	// PodmanSocketEnv is the environment variable for custom Podman socket path
	PodmanSocketEnv = "TOOLHIVE_PODMAN_SOCKET"
)

var supportedSocketPaths = []runtime.Type{runtime.TypePodman, runtime.TypeDocker}

// Client implements the Runtime interface for container operations
type Client struct {
	runtimeType runtime.Type
	socketPath  string
	client      *client.Client
}

// NewClient creates a new container client
func NewClient(ctx context.Context) (*Client, error) {
	var lastErr error

	// We try to find a container socket for the given runtime
	// We try Podman first, then Docker as fallback
	// Once a socket is found, we create a client and ping the runtime
	// If the ping fails, we try the next runtime
	// If all runtimes fail, we return an error
	for _, sp := range supportedSocketPaths {
		// Try to find a container socket for the given runtime
		socketPath, runtimeType, err := findContainerSocket(sp)
		if err != nil {
			logger.Debugf("Failed to find socket for %s: %v", sp, err)
			lastErr = err
			continue
		}

		c, err := NewClientWithSocketPath(ctx, socketPath, runtimeType)
		if err != nil {
			logger.Debugf("Failed to create client for %s: %v", sp, err)
			lastErr = err
			continue
		}

		return c, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("no supported container runtime available: %w", lastErr)
	}
	return nil, fmt.Errorf("no supported container runtime found/running")
}

// NewClientWithSocketPath creates a new container client with a specific socket path
func NewClientWithSocketPath(ctx context.Context, socketPath string, runtimeType runtime.Type) (*Client, error) {
	// Create platform-specific client
	_, opts := newPlatformClient(socketPath)

	// Create Docker client with the custom HTTP client
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
	logger.Debugf("Successfully connected to %s runtime", c.runtimeType)

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
func findContainerSocket(rt runtime.Type) (string, runtime.Type, error) {
	// Use platform-specific implementation
	return findPlatformContainerSocket(rt)
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
func convertMounts(mounts []runtime.Mount) []mount.Mount {
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
func setupPortBindings(hostConfig *container.HostConfig, portBindings map[string][]runtime.PortBinding) error {
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

// DeployWorkload creates and starts a workload.
// It configures the workload based on the provided permission profile and transport type.
// If options is nil, default options will be used.
func (c *Client) DeployWorkload(
	ctx context.Context,
	image, name string,
	command []string,
	envVars, labels map[string]string,
	permissionProfile *permissions.Profile,
	transportType string,
	options *runtime.DeployWorkloadOptions,
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
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped",
		},
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

	// Check if container with this name already exists
	existingID, err := c.findExistingContainer(ctx, name)
	if err != nil {
		return "", err
	}

	// If container exists, check if we need to recreate it
	if existingID != "" {
		canReuse, err := c.handleExistingContainer(ctx, existingID, config, hostConfig)
		if err != nil {
			return "", err
		}

		if canReuse {
			// Container exists with the right configuration, return its ID
			return existingID, nil
		}
		// Container was removed and needs to be recreated
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

	// Start the container
	err = c.client.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if err != nil {
		return "", NewContainerError(err, resp.ID, fmt.Sprintf("failed to start container: %v", err))
	}

	return resp.ID, nil
}

// ListWorkloads lists workloads
func (c *Client) ListWorkloads(ctx context.Context) ([]runtime.ContainerInfo, error) {
	// Create filter for toolhive containers
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "toolhive=true")

	// List containers
	containers, err := c.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, NewContainerError(err, "", fmt.Sprintf("failed to list containers: %v", err))
	}

	// Convert to our ContainerInfo format
	result := make([]runtime.ContainerInfo, 0, len(containers))
	for _, c := range containers {
		// Extract container name (remove leading slash)
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
			name = strings.TrimPrefix(name, "/")
		}

		// Extract port mappings
		ports := make([]runtime.PortMapping, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, runtime.PortMapping{
				ContainerPort: int(p.PrivatePort),
				HostPort:      int(p.PublicPort),
				Protocol:      p.Type,
			})
		}

		// Convert creation time
		created := time.Unix(c.Created, 0)

		result = append(result, runtime.ContainerInfo{
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

// StopWorkload stops a workload
// If the workload is already stopped, it returns success
func (c *Client) StopWorkload(ctx context.Context, workloadID string) error {
	// Check if the workload is running
	running, err := c.IsWorkloadRunning(ctx, workloadID)
	if err != nil {
		// If the container doesn't exist, that's fine - it's already "stopped"
		if err, ok := err.(*ContainerError); ok && err.Err == ErrContainerNotFound {
			return nil
		}
		return err
	}

	// If the container is not running, return success
	if !running {
		return nil
	}

	// Use a reasonable timeout
	timeoutSeconds := 30
	err = c.client.ContainerStop(ctx, workloadID, container.StopOptions{Timeout: &timeoutSeconds})
	if err != nil {
		return NewContainerError(err, workloadID, fmt.Sprintf("failed to stop workload: %v", err))
	}
	return nil
}

// RemoveWorkload removes a workload
// If the workload doesn't exist, it returns success
func (c *Client) RemoveWorkload(ctx context.Context, workloadID string) error {
	err := c.client.ContainerRemove(ctx, workloadID, container.RemoveOptions{
		Force: true,
	})
	if err != nil {
		// If the workload doesn't exist, that's fine - it's already removed
		if client.IsErrNotFound(err) {
			return nil
		}
		return NewContainerError(err, workloadID, fmt.Sprintf("failed to remove workload: %v", err))
	}
	return nil
}

// GetWorkloadLogs gets workload logs
func (c *Client) GetWorkloadLogs(ctx context.Context, workloadID string, follow bool) (string, error) {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       "100",
	}

	// Get logs
	logs, err := c.client.ContainerLogs(ctx, workloadID, options)
	if err != nil {
		return "", NewContainerError(err, workloadID, fmt.Sprintf("failed to get workload logs: %v", err))
	}
	defer logs.Close()

	if follow {
		_, err = io.Copy(os.Stdout, logs)
		if err != nil && err != io.EOF {
			logger.Errorf("Error reading container logs: %v", err)
			return "", NewContainerError(err, workloadID, fmt.Sprintf("failed to follow workload logs: %v", err))
		}
	}

	// Read logs
	logBytes, err := io.ReadAll(logs)
	if err != nil {
		return "", NewContainerError(err, workloadID, fmt.Sprintf("failed to read workload logs: %v", err))
	}

	return string(logBytes), nil
}

// IsWorkloadRunning checks if a workload is running
func (c *Client) IsWorkloadRunning(ctx context.Context, workloadID string) (bool, error) {
	// Inspect workload
	info, err := c.client.ContainerInspect(ctx, workloadID)
	if err != nil {
		// Check if the error is because the workload doesn't exist
		if client.IsErrNotFound(err) {
			return false, NewContainerError(ErrContainerNotFound, workloadID, "workload not found")
		}
		return false, NewContainerError(err, workloadID, fmt.Sprintf("failed to inspect workload: %v", err))
	}

	return info.State.Running, nil
}

// GetWorkloadInfo gets workload information
func (c *Client) GetWorkloadInfo(ctx context.Context, workloadID string) (runtime.ContainerInfo, error) {
	// Inspect workload
	info, err := c.client.ContainerInspect(ctx, workloadID)
	if err != nil {
		// Check if the error is because the workload doesn't exist
		if client.IsErrNotFound(err) {
			return runtime.ContainerInfo{}, NewContainerError(ErrContainerNotFound, workloadID, "workload not found")
		}
		return runtime.ContainerInfo{}, NewContainerError(err, workloadID, fmt.Sprintf("failed to inspect workload: %v", err))
	}

	// Extract port mappings
	ports := make([]runtime.PortMapping, 0)
	for containerPort, bindings := range info.NetworkSettings.Ports {
		for _, binding := range bindings {
			hostPort := 0
			if _, err := fmt.Sscanf(binding.HostPort, "%d", &hostPort); err != nil {
				// If we can't parse the port, just use 0
				logger.Warnf("Warning: Failed to parse host port %s: %v", binding.HostPort, err)
			}

			ports = append(ports, runtime.PortMapping{
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

	return runtime.ContainerInfo{
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

// AttachToWorkload attaches to a workload
func (c *Client) AttachToWorkload(ctx context.Context, workloadID string) (io.WriteCloser, io.ReadCloser, error) {
	// Check if workload exists and is running
	running, err := c.IsWorkloadRunning(ctx, workloadID)
	if err != nil {
		return nil, nil, err
	}
	if !running {
		return nil, nil, NewContainerError(ErrContainerNotRunning, workloadID, "workload is not running")
	}

	// Attach to workload
	resp, err := c.client.ContainerAttach(ctx, workloadID, container.AttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return nil, nil, NewContainerError(ErrAttachFailed, workloadID, fmt.Sprintf("failed to attach to workload: %v", err))
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

// parsePullOutput parses the Docker image pull output and formats it in a more readable way
func parsePullOutput(reader io.Reader, writer io.Writer) error {
	decoder := json.NewDecoder(reader)
	for {
		var pullStatus struct {
			Status         string          `json:"status"`
			ID             string          `json:"id,omitempty"`
			ProgressDetail json.RawMessage `json:"progressDetail,omitempty"`
			Progress       string          `json:"progress,omitempty"`
		}

		if err := decoder.Decode(&pullStatus); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode pull output: %w", err)
		}

		// Format the output based on the type of message
		if pullStatus.Progress != "" {
			// This is a progress update
			fmt.Fprintf(writer, "%s: %s %s\n", pullStatus.Status, pullStatus.ID, pullStatus.Progress)
		} else if pullStatus.ID != "" {
			// This is a layer-specific status update
			fmt.Fprintf(writer, "%s: %s\n", pullStatus.Status, pullStatus.ID)
		} else {
			// This is a general status update
			fmt.Fprintf(writer, "%s\n", pullStatus.Status)
		}
	}

	return nil
}

// PullImage pulls an image from a registry
func (c *Client) PullImage(ctx context.Context, imageName string) error {
	logger.Infof("Pulling image: %s", imageName)

	// Pull the image
	reader, err := c.client.ImagePull(ctx, imageName, dockerimage.PullOptions{})
	if err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to pull image: %v", err))
	}
	defer reader.Close()

	// Parse and filter the pull output
	if err := parsePullOutput(reader, os.Stdout); err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to process pull output: %v", err))
	}

	return nil
}

// VerifyImage verifies a container image
func (*Client) VerifyImage(_ context.Context, serverInfo *registry.Server, imageRef string) (bool, error) {
	// Create a new verifier
	v, err := verifier.New(serverInfo)
	if err != nil {
		return false, err
	}

	// Verify the image passing the server info
	return v.VerifyServer(imageRef, serverInfo)
}

// BuildImage builds a Docker image from a Dockerfile in the specified context directory
func (c *Client) BuildImage(ctx context.Context, contextDir, imageName string) error {
	logger.Infof("Building image %s from context directory %s", imageName, contextDir)

	// Create a tar archive of the context directory
	tarFile, err := os.CreateTemp("", "docker-build-context-*.tar")
	if err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to create temporary tar file: %v", err))
	}
	defer os.Remove(tarFile.Name())
	defer tarFile.Close()

	// Create a tar archive of the context directory
	if err := createTarFromDir(contextDir, tarFile); err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to create tar archive: %v", err))
	}

	// Reset the file pointer to the beginning of the file
	if _, err := tarFile.Seek(0, 0); err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to reset tar file pointer: %v", err))
	}

	// Build the image
	buildOptions := types.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	response, err := c.client.ImageBuild(ctx, tarFile, buildOptions)
	if err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to build image: %v", err))
	}
	defer response.Body.Close()

	// Parse and log the build output
	if err := parseBuildOutput(response.Body, os.Stdout); err != nil {
		return NewContainerError(err, "", fmt.Sprintf("failed to process build output: %v", err))
	}

	return nil
}

// createTarFromDir creates a tar archive from a directory
func createTarFromDir(srcDir string, writer io.Writer) error {
	// Create a new tar writer
	tw := tar.NewWriter(writer)
	defer tw.Close()

	// Walk through the directory and add files to the tar archive
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Skip the root directory
		if relPath == "." {
			return nil
		}

		// Create a tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header: %w", err)
		}

		// Set the name to the relative path
		header.Name = relPath

		// Write the header
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}

		// If it's a regular file, write the contents
		if !info.IsDir() {
			// #nosec G304 - This is safe because we're only opening files within the specified context directory
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file: %w", err)
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return fmt.Errorf("failed to copy file contents: %w", err)
			}
		}

		return nil
	})
}

// parseBuildOutput parses the Docker image build output and formats it in a more readable way
func parseBuildOutput(reader io.Reader, writer io.Writer) error {
	decoder := json.NewDecoder(reader)
	for {
		var buildOutput struct {
			Stream string `json:"stream,omitempty"`
			Error  string `json:"error,omitempty"`
		}

		if err := decoder.Decode(&buildOutput); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode build output: %w", err)
		}

		// Check for errors
		if buildOutput.Error != "" {
			return fmt.Errorf("build error: %s", buildOutput.Error)
		}

		// Print the stream output
		if buildOutput.Stream != "" {
			fmt.Fprint(writer, buildOutput.Stream)
		}
	}

	return nil
}

// getPermissionConfigFromProfile converts a permission profile to a container permission config
// with transport-specific settings (internal function)
// addReadOnlyMounts adds read-only mounts to the permission config
func (*Client) addReadOnlyMounts(config *runtime.PermissionConfig, mounts []permissions.MountDeclaration) {
	for _, mountDecl := range mounts {
		source, target, err := mountDecl.Parse()
		if err != nil {
			// Skip invalid mounts
			logger.Warnf("Warning: Skipping invalid mount declaration: %s (%v)", mountDecl, err)
			continue
		}

		// Skip resource URIs for now (they need special handling)
		if strings.Contains(source, "://") {
			logger.Warnf("Warning: Resource URI mounts not yet supported: %s", source)
			continue
		}

		// Convert relative paths to absolute paths
		absPath, ok := convertRelativePathToAbsolute(source, mountDecl)
		if !ok {
			continue
		}

		config.Mounts = append(config.Mounts, runtime.Mount{
			Source:   absPath,
			Target:   target,
			ReadOnly: true,
		})
	}
}

// addReadWriteMounts adds read-write mounts to the permission config
func (*Client) addReadWriteMounts(config *runtime.PermissionConfig, mounts []permissions.MountDeclaration) {
	for _, mountDecl := range mounts {
		source, target, err := mountDecl.Parse()
		if err != nil {
			// Skip invalid mounts
			logger.Warnf("Warning: Skipping invalid mount declaration: %s (%v)", mountDecl, err)
			continue
		}

		// Skip resource URIs for now (they need special handling)
		if strings.Contains(source, "://") {
			logger.Warnf("Warning: Resource URI mounts not yet supported: %s", source)
			continue
		}

		// Convert relative paths to absolute paths
		absPath, ok := convertRelativePathToAbsolute(source, mountDecl)
		if !ok {
			continue
		}

		// Check if the path is already mounted read-only
		alreadyMounted := false
		for i, m := range config.Mounts {
			if m.Target == target {
				// Update the mount to be read-write
				config.Mounts[i].ReadOnly = false
				alreadyMounted = true
				break
			}
		}

		// If not already mounted, add a new mount
		if !alreadyMounted {
			config.Mounts = append(config.Mounts, runtime.Mount{
				Source:   absPath,
				Target:   target,
				ReadOnly: false,
			})
		}
	}
}

// convertRelativePathToAbsolute converts a relative path to an absolute path
// Returns the absolute path and a boolean indicating if the conversion was successful
func convertRelativePathToAbsolute(source string, mountDecl permissions.MountDeclaration) (string, bool) {
	// If it's already an absolute path, return it as is
	if filepath.IsAbs(source) {
		return source, true
	}

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		logger.Warnf("Warning: Failed to get current working directory: %v", err)
		return "", false
	}

	// Convert relative path to absolute path
	absPath := filepath.Join(cwd, source)
	logger.Infof("Converting relative path to absolute: %s -> %s", mountDecl, absPath)
	return absPath, true
}

// needsNetworkAccess determines if the container needs network access
func (*Client) needsNetworkAccess(profile *permissions.Profile, transportType string) bool {
	// SSE transport always needs network access
	if transportType == "sse" || transportType == "inspector" {
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

// getPermissionConfigFromProfile converts a permission profile to a container permission config
func (c *Client) getPermissionConfigFromProfile(
	profile *permissions.Profile,
	transportType string,
) (*runtime.PermissionConfig, error) {
	// Start with a default permission config
	config := &runtime.PermissionConfig{
		Mounts:      []runtime.Mount{},
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
	if transportType != "sse" && transportType != "stdio" && transportType != "inspector" {
		return nil, fmt.Errorf("unsupported transport type: %s", transportType)
	}

	return config, nil
}

// Error types for container operations
var (
	// ErrContainerNotFound is returned when a container is not found
	ErrContainerNotFound = fmt.Errorf("container not found")

	// ErrContainerAlreadyExists is returned when a container already exists
	ErrContainerAlreadyExists = fmt.Errorf("container already exists")

	// ErrContainerNotRunning is returned when a container is not running
	ErrContainerNotRunning = fmt.Errorf("container not running")

	// ErrContainerAlreadyRunning is returned when a container is already running
	ErrContainerAlreadyRunning = fmt.Errorf("container already running")

	// ErrRuntimeNotFound is returned when a container runtime is not found
	ErrRuntimeNotFound = fmt.Errorf("container runtime not found")

	// ErrInvalidRuntimeType is returned when an invalid runtime type is specified
	ErrInvalidRuntimeType = fmt.Errorf("invalid runtime type")

	// ErrAttachFailed is returned when attaching to a container fails
	ErrAttachFailed = fmt.Errorf("failed to attach to container")

	// ErrContainerExited is returned when a container has exited unexpectedly
	ErrContainerExited = fmt.Errorf("container exited unexpectedly")
)

// ContainerError represents an error related to container operations
type ContainerError struct {
	// Err is the underlying error
	Err error
	// ContainerID is the ID of the container
	ContainerID string
	// Message is an optional error message
	Message string
}

// Error returns the error message
func (e *ContainerError) Error() string {
	if e.Message != "" {
		if e.ContainerID != "" {
			return fmt.Sprintf("%s: %s (container: %s)", e.Err, e.Message, e.ContainerID)
		}
		return fmt.Sprintf("%s: %s", e.Err, e.Message)
	}

	if e.ContainerID != "" {
		return fmt.Sprintf("%s (container: %s)", e.Err, e.ContainerID)
	}

	return e.Err.Error()
}

// Unwrap returns the underlying error
func (e *ContainerError) Unwrap() error {
	return e.Err
}

// NewContainerError creates a new container error
func NewContainerError(err error, containerID, message string) *ContainerError {
	return &ContainerError{
		Err:         err,
		ContainerID: containerID,
		Message:     message,
	}
}

// findExistingContainer finds a container with the exact name
func (c *Client) findExistingContainer(ctx context.Context, name string) (string, error) {
	containers, err := c.client.ContainerList(ctx, container.ListOptions{
		All: true, // Include stopped containers
		Filters: filters.NewArgs(
			filters.Arg("name", name),
		),
	})
	if err != nil {
		return "", NewContainerError(err, "", fmt.Sprintf("failed to list containers: %v", err))
	}

	// Find exact name match (filter can return partial matches)
	for _, cont := range containers {
		for _, containerName := range cont.Names {
			// Container names in the API have a leading slash
			if containerName == "/"+name || containerName == name {
				return cont.ID, nil
			}
		}
	}

	return "", nil
}

// compareBasicConfig compares basic container configuration (image, command, env vars, labels, stdio settings)
func compareBasicConfig(existing *container.InspectResponse, desired *container.Config) bool {
	// Compare image
	if existing.Config.Image != desired.Image {
		return false
	}

	// Compare command
	if len(existing.Config.Cmd) != len(desired.Cmd) {
		return false
	}
	for i, cmd := range existing.Config.Cmd {
		if i >= len(desired.Cmd) || cmd != desired.Cmd[i] {
			return false
		}
	}

	// Compare environment variables
	if !compareEnvVars(existing.Config.Env, desired.Env) {
		return false
	}

	// Compare labels
	if !compareLabels(existing.Config.Labels, desired.Labels) {
		return false
	}

	// Compare stdio settings
	if existing.Config.AttachStdin != desired.AttachStdin ||
		existing.Config.AttachStdout != desired.AttachStdout ||
		existing.Config.AttachStderr != desired.AttachStderr ||
		existing.Config.OpenStdin != desired.OpenStdin {
		return false
	}

	return true
}

// compareEnvVars compares environment variables
func compareEnvVars(existingEnv, desiredEnv []string) bool {
	// Convert to maps for easier comparison
	existingMap := envSliceToMap(existingEnv)
	desiredMap := envSliceToMap(desiredEnv)

	// Check if all desired env vars are in existing env with correct values
	for k, v := range desiredMap {
		existingVal, exists := existingMap[k]
		if !exists || existingVal != v {
			return false
		}
	}

	return true
}

// envSliceToMap converts a slice of environment variables to a map
func envSliceToMap(env []string) map[string]string {
	result := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

// compareLabels compares container labels
func compareLabels(existingLabels, desiredLabels map[string]string) bool {
	// Check if all desired labels are in existing labels with correct values
	for k, v := range desiredLabels {
		existingVal, exists := existingLabels[k]
		if !exists || existingVal != v {
			return false
		}
	}
	return true
}

// compareHostConfig compares host configuration (network mode, capabilities, security options)
func compareHostConfig(existing *container.InspectResponse, desired *container.HostConfig) bool {
	// Compare network mode
	if string(existing.HostConfig.NetworkMode) != string(desired.NetworkMode) {
		return false
	}

	// Compare capabilities
	if !compareStringSlices(existing.HostConfig.CapAdd, desired.CapAdd) {
		return false
	}
	if !compareStringSlices(existing.HostConfig.CapDrop, desired.CapDrop) {
		return false
	}

	// Compare security options
	if !compareStringSlices(existing.HostConfig.SecurityOpt, desired.SecurityOpt) {
		return false
	}

	// Compare restart policy
	if existing.HostConfig.RestartPolicy.Name != desired.RestartPolicy.Name {
		return false
	}

	return true
}

// compareStringSlices compares two string slices
func compareStringSlices(existing, desired []string) bool {
	if len(existing) != len(desired) {
		return false
	}
	for i, s := range existing {
		if i >= len(desired) || s != desired[i] {
			return false
		}
	}
	return true
}

// compareMounts compares volume mounts
func compareMounts(existing *container.InspectResponse, desired *container.HostConfig) bool {
	if len(existing.HostConfig.Mounts) != len(desired.Mounts) {
		return false
	}

	// Create maps by target path for easier comparison
	existingMountsMap := make(map[string]mount.Mount)
	for _, m := range existing.HostConfig.Mounts {
		existingMountsMap[m.Target] = m
	}

	// Check if all desired mounts exist in the container with matching source and read-only flag
	for _, desiredMount := range desired.Mounts {
		existingMount, exists := existingMountsMap[desiredMount.Target]
		if !exists || existingMount.Source != desiredMount.Source || existingMount.ReadOnly != desiredMount.ReadOnly {
			return false
		}
	}

	return true
}

// comparePortConfig compares port configuration (exposed ports and port bindings)
func comparePortConfig(existing *container.InspectResponse, desired *container.Config, desiredHost *container.HostConfig) bool {
	// Compare exposed ports
	if len(existing.Config.ExposedPorts) != len(desired.ExposedPorts) {
		return false
	}
	for port := range desired.ExposedPorts {
		if _, exists := existing.Config.ExposedPorts[port]; !exists {
			return false
		}
	}

	// Compare port bindings
	if len(existing.HostConfig.PortBindings) != len(desiredHost.PortBindings) {
		return false
	}
	for port, bindings := range desiredHost.PortBindings {
		existingBindings, exists := existing.HostConfig.PortBindings[port]
		if !exists || len(existingBindings) != len(bindings) {
			return false
		}
		for i, binding := range bindings {
			if i >= len(existingBindings) ||
				existingBindings[i].HostIP != binding.HostIP ||
				existingBindings[i].HostPort != binding.HostPort {
				return false
			}
		}
	}

	return true
}

// compareContainerConfig compares an existing container's configuration with the desired configuration
func compareContainerConfig(
	existing *container.InspectResponse,
	desired *container.Config,
	desiredHost *container.HostConfig,
) bool {
	// Compare basic configuration
	if !compareBasicConfig(existing, desired) {
		return false
	}

	// Compare host configuration
	if !compareHostConfig(existing, desiredHost) {
		return false
	}

	// Compare mounts
	if !compareMounts(existing, desiredHost) {
		return false
	}

	// Compare port configuration
	if !comparePortConfig(existing, desired, desiredHost) {
		return false
	}

	// All checks passed, configurations match
	return true
}

// handleExistingContainer checks if an existing container's configuration matches the desired configuration
// Returns true if the container can be reused, false if it was removed and needs to be recreated
func (c *Client) handleExistingContainer(
	ctx context.Context,
	containerID string,
	desiredConfig *container.Config,
	desiredHostConfig *container.HostConfig,
) (bool, error) {
	// Get container info
	info, err := c.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, NewContainerError(err, containerID, fmt.Sprintf("failed to inspect container: %v", err))
	}

	// Compare configurations
	if compareContainerConfig(&info, desiredConfig, desiredHostConfig) {
		// Configurations match, container can be reused

		// Check if the container is running
		if !info.State.Running {
			// Container exists but is not running, start it
			err = c.client.ContainerStart(ctx, containerID, container.StartOptions{})
			if err != nil {
				return false, NewContainerError(err, containerID, fmt.Sprintf("failed to start existing container: %v", err))
			}
		}

		return true, nil
	}

	// Configurations don't match, need to recreate the container
	// Stop the workload
	if err := c.StopWorkload(ctx, containerID); err != nil {
		return false, err
	}

	// Remove the workload
	if err := c.RemoveWorkload(ctx, containerID); err != nil {
		return false, err
	}

	// Container was removed and needs to be recreated
	return false, nil
}

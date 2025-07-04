// Package workloads contains high-level logic for managing the lifecycle of
// ToolHive-managed containers.
package workloads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"golang.org/x/sync/errgroup"

	"github.com/stacklok/toolhive/pkg/client"
	"github.com/stacklok/toolhive/pkg/config"
	ct "github.com/stacklok/toolhive/pkg/container"
	rt "github.com/stacklok/toolhive/pkg/container/runtime"
	"github.com/stacklok/toolhive/pkg/labels"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/process"
	"github.com/stacklok/toolhive/pkg/runner"
	"github.com/stacklok/toolhive/pkg/secrets"
	"github.com/stacklok/toolhive/pkg/transport/proxy"
)

// Manager is responsible for managing the state of ToolHive-managed containers.
type Manager interface {
	// GetWorkload returns information about the named container.
	GetWorkload(ctx context.Context, name string) (Workload, error)
	// ListWorkloads lists all ToolHive-managed containers.
	ListWorkloads(ctx context.Context, listAll bool) ([]Workload, error)
	// DeleteWorkloads deletes the specified workloads by name.
	// It is implemented as an asynchronous operation which returns an errgroup.Group
	DeleteWorkloads(ctx context.Context, names []string) (*errgroup.Group, error)
	// StopWorkloads stops the specified workloads by name.
	// It is implemented as an asynchronous operation which returns an errgroup.Group
	StopWorkloads(ctx context.Context, names []string) (*errgroup.Group, error)
	// RunWorkload runs a container in the foreground.
	RunWorkload(ctx context.Context, runConfig *runner.RunConfig) error
	// RunWorkloadDetached runs a container in the background.
	RunWorkloadDetached(runConfig *runner.RunConfig) error
	// RestartWorkloads restarts the specified workloads by name.
	// It is implemented as an asynchronous operation which returns an errgroup.Group
	RestartWorkloads(ctx context.Context, names []string) (*errgroup.Group, error)
	// GetLogs retrieves the logs of a container.
	GetLogs(ctx context.Context, containerName string, follow bool) (string, error)
}

type defaultManager struct {
	runtime rt.Runtime
}

// ErrContainerNotFound is returned when a container cannot be found by name.
// ErrInvalidWorkloadName is returned when a workload name fails validation.
var (
	ErrContainerNotFound   = fmt.Errorf("container not found")
	ErrContainerNotRunning = fmt.Errorf("container not running")
	ErrInvalidWorkloadName = fmt.Errorf("invalid workload name")
)

const (
	// AsyncOperationTimeout is the timeout for async workload operations
	AsyncOperationTimeout = 5 * time.Minute
)

// validateWorkloadName validates workload names to prevent path traversal attacks
// and other security issues. Workload names should only contain alphanumeric
// characters, hyphens, underscores, and dots.
var workloadNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// NewManager creates a new container manager instance.
func NewManager(ctx context.Context) (Manager, error) {
	runtime, err := ct.NewFactory().Create(ctx)
	if err != nil {
		return nil, err
	}

	return &defaultManager{
		runtime: runtime,
	}, nil
}

// NewManagerFromRuntime creates a new container manager instance from an existing runtime.
func NewManagerFromRuntime(runtime rt.Runtime) Manager {
	return &defaultManager{
		runtime: runtime,
	}
}

func (d *defaultManager) GetWorkload(ctx context.Context, name string) (Workload, error) {
	// Validate workload name to prevent path traversal attacks
	if err := validateWorkloadName(name); err != nil {
		return Workload{}, err
	}

	container, err := d.findContainerByName(ctx, name)
	if err != nil {
		// Note that `findContainerByName` already wraps the error with a more specific message.
		return Workload{}, err
	}

	return WorkloadFromContainerInfo(container)
}

func (d *defaultManager) ListWorkloads(ctx context.Context, listAll bool) ([]Workload, error) {
	// List containers
	containers, err := d.runtime.ListWorkloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %v", err)
	}

	// Filter containers to only show those managed by ToolHive
	var workloads []Workload
	for _, c := range containers {
		// If the caller did not set `listAll` to true, only include running containers.
		if labels.IsToolHiveContainer(c.Labels) && (isContainerRunning(&c) || listAll) {
			workload, err := WorkloadFromContainerInfo(&c)
			if err != nil {
				return nil, err
			}
			workloads = append(workloads, workload)
		}
	}

	return workloads, nil
}

func (d *defaultManager) StopWorkloads(ctx context.Context, names []string) (*errgroup.Group, error) {
	// Validate all workload names to prevent path traversal attacks
	for _, name := range names {
		if err := validateWorkloadName(name); err != nil {
			return nil, fmt.Errorf("invalid workload name '%s': %w", name, err)
		}
	}

	// Find all containers first
	var containers []*rt.ContainerInfo
	for _, name := range names {
		container, err := d.findContainerByName(ctx, name)
		if err != nil {
			if errors.Is(err, ErrContainerNotFound) {
				// Log but don't fail the entire operation for not found containers
				logger.Warnf("Warning: Failed to stop workload %s: %v", name, err)
				continue
			}
			return nil, fmt.Errorf("failed to find workload %s: %v", name, err)
		}

		running := isContainerRunning(container)
		if !running {
			// Log but don't fail the entire operation for not running containers
			logger.Warnf("Warning: Failed to stop workload %s: %v", name, ErrContainerNotRunning)
			continue
		}

		containers = append(containers, container)
	}

	return d.stopWorkloads(ctx, containers), nil
}

func (*defaultManager) RunWorkload(ctx context.Context, runConfig *runner.RunConfig) error {
	mcpRunner := runner.NewRunner(runConfig)
	return mcpRunner.Run(ctx)
}

//nolint:gocyclo // This function is complex but manageable
func (*defaultManager) RunWorkloadDetached(runConfig *runner.RunConfig) error {
	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	// Create a log file for the detached process
	logFilePath, err := xdg.DataFile(fmt.Sprintf("toolhive/logs/%s.log", runConfig.BaseName))
	if err != nil {
		return fmt.Errorf("failed to create log file path: %v", err)
	}
	// #nosec G304 - This is safe as baseName is generated by the application
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		logger.Warnf("Warning: Failed to create log file: %v", err)
	} else {
		defer logFile.Close()
		logger.Infof("Logging to: %s", logFilePath)
	}

	// Prepare the command arguments for the detached process
	// We'll run the same command but with the --foreground flag
	detachedArgs := []string{"run", "--foreground"}

	// Add all the original flags
	if runConfig.Transport != "stdio" {
		detachedArgs = append(detachedArgs, "--transport", string(runConfig.Transport))
	}

	// Add proxy-mode if set
	if runConfig.ProxyMode != "" {
		detachedArgs = append(detachedArgs, "--proxy-mode", runConfig.ProxyMode.String())
	}

	if runConfig.Debug {
		detachedArgs = append(detachedArgs, "--debug")
	}

	if runConfig.IsolateNetwork {
		detachedArgs = append(detachedArgs, "--isolate-network")
	}

	// Use Name if available
	if runConfig.Name != "" {
		detachedArgs = append(detachedArgs, "--name", runConfig.Name)
	}

	// Use ContainerName if available
	if runConfig.ContainerName != "" {
		detachedArgs = append(detachedArgs, "--name", runConfig.ContainerName)
	}

	if runConfig.Host != "" {
		detachedArgs = append(detachedArgs, "--host", runConfig.Host)
	}

	if runConfig.Port != 0 {
		detachedArgs = append(detachedArgs, "--port", strconv.Itoa(runConfig.Port))
	}

	if runConfig.TargetPort != 0 {
		detachedArgs = append(detachedArgs, "--target-port", strconv.Itoa(runConfig.TargetPort))
	}

	// Add target host if it's not the default
	if runConfig.TargetHost != "localhost" {
		detachedArgs = append(detachedArgs, "--target-host", runConfig.TargetHost)
	}

	// Pass the permission profile to the detached process
	if runConfig.PermissionProfile != nil {
		// We need to create a temporary file for the permission profile
		permProfilePath, err := runner.CreatePermissionProfileFile(runConfig.BaseName, runConfig.PermissionProfile)
		if err != nil {
			logger.Warnf("Warning: Failed to create permission profile file: %v", err)
		} else {
			detachedArgs = append(detachedArgs, "--permission-profile", permProfilePath)
		}
	}

	// Add environment variables
	for key, value := range runConfig.EnvVars {
		detachedArgs = append(detachedArgs, "--env", fmt.Sprintf("%s=%s", key, value))
	}

	// Add volume mounts if they were provided
	for _, volume := range runConfig.Volumes {
		detachedArgs = append(detachedArgs, "--volume", volume)
	}

	// Add secrets if they were provided
	for _, secret := range runConfig.Secrets {
		detachedArgs = append(detachedArgs, "--secret", secret)
	}

	// Add OIDC flags if they were provided
	if runConfig.OIDCConfig != nil {
		if runConfig.OIDCConfig.Issuer != "" {
			detachedArgs = append(detachedArgs, "--oidc-issuer", runConfig.OIDCConfig.Issuer)
		}
		if runConfig.OIDCConfig.Audience != "" {
			detachedArgs = append(detachedArgs, "--oidc-audience", runConfig.OIDCConfig.Audience)
		}
		if runConfig.OIDCConfig.JWKSURL != "" {
			detachedArgs = append(detachedArgs, "--oidc-jwks-url", runConfig.OIDCConfig.JWKSURL)
		}
		if runConfig.OIDCConfig.ClientID != "" {
			detachedArgs = append(detachedArgs, "--oidc-client-id", runConfig.OIDCConfig.ClientID)
		}
	}

	// Add authz config if it was provided
	if runConfig.AuthzConfigPath != "" {
		detachedArgs = append(detachedArgs, "--authz-config", runConfig.AuthzConfigPath)
	}

	// Add audit config if it was provided
	if runConfig.AuditConfigPath != "" {
		detachedArgs = append(detachedArgs, "--audit-config", runConfig.AuditConfigPath)
	}

	// Add telemetry flags if telemetry config is provided
	if runConfig.TelemetryConfig != nil {
		if runConfig.TelemetryConfig.Endpoint != "" {
			detachedArgs = append(detachedArgs, "--otel-endpoint", runConfig.TelemetryConfig.Endpoint)
		}
		if runConfig.TelemetryConfig.ServiceName != "" {
			detachedArgs = append(detachedArgs, "--otel-service-name", runConfig.TelemetryConfig.ServiceName)
		}
		if runConfig.TelemetryConfig.SamplingRate != 0.1 { // Only add if not default
			detachedArgs = append(detachedArgs, "--otel-sampling-rate", fmt.Sprintf("%f", runConfig.TelemetryConfig.SamplingRate))
		}
		for key, value := range runConfig.TelemetryConfig.Headers {
			detachedArgs = append(detachedArgs, "--otel-headers", fmt.Sprintf("%s=%s", key, value))
		}
		if runConfig.TelemetryConfig.Insecure {
			detachedArgs = append(detachedArgs, "--otel-insecure")
		}
		if runConfig.TelemetryConfig.EnablePrometheusMetricsPath {
			detachedArgs = append(detachedArgs, "--otel-enable-prometheus-metrics-path")
		}
		for _, envVar := range runConfig.TelemetryConfig.EnvironmentVariables {
			detachedArgs = append(detachedArgs, "--otel-env-vars", envVar)
		}
	}

	// Add enable audit flag if audit config is set but no config path is provided
	if runConfig.AuditConfig != nil && runConfig.AuditConfigPath == "" {
		detachedArgs = append(detachedArgs, "--enable-audit")
	}

	// Add the image and any arguments
	detachedArgs = append(detachedArgs, runConfig.Image)
	if len(runConfig.CmdArgs) > 0 {
		detachedArgs = append(detachedArgs, "--")
		detachedArgs = append(detachedArgs, runConfig.CmdArgs...)
	}

	// Create a new command
	// #nosec G204 - This is safe as execPath is the path to the current binary
	detachedCmd := exec.Command(execPath, detachedArgs...)

	// Set environment variables for the detached process
	detachedCmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", process.ToolHiveDetachedEnv, process.ToolHiveDetachedValue))

	// If we need the decrypt password, set it as an environment variable in the detached process.
	// NOTE: This breaks the abstraction slightly since this is only relevant for the CLI, but there
	// are checks inside `GetSecretsPassword` to ensure this does not get called in a detached process.
	// This will be addressed in a future re-think of the secrets manager interface.
	if needSecretsPassword(runConfig.Secrets) {
		password, err := secrets.GetSecretsPassword("")
		if err != nil {
			return fmt.Errorf("failed to get secrets password: %v", err)
		}
		detachedCmd.Env = append(detachedCmd.Env, fmt.Sprintf("%s=%s", secrets.PasswordEnvVar, password))
	}

	// Redirect stdout and stderr to the log file if it was created successfully
	if logFile != nil {
		detachedCmd.Stdout = logFile
		detachedCmd.Stderr = logFile
	} else {
		// Otherwise, discard the output
		detachedCmd.Stdout = nil
		detachedCmd.Stderr = nil
	}

	// Detach the process from the terminal
	detachedCmd.Stdin = nil
	detachedCmd.SysProcAttr = getSysProcAttr()

	// Start the detached process
	if err := detachedCmd.Start(); err != nil {
		return fmt.Errorf("failed to start detached process: %v", err)
	}

	// Write the PID to a file so the stop command can kill the process
	if err := process.WritePIDFile(runConfig.BaseName, detachedCmd.Process.Pid); err != nil {
		logger.Warnf("Warning: Failed to write PID file: %v", err)
	}

	logger.Infof("MCP server is running in the background (PID: %d)", detachedCmd.Process.Pid)
	logger.Infof("Use 'thv stop %s' to stop the server", runConfig.ContainerName)

	return nil
}

func (d *defaultManager) GetLogs(ctx context.Context, containerName string, follow bool) (string, error) {
	container, err := d.findContainerByName(ctx, containerName)
	if err != nil {
		// Propagate the error if the container is not found
		if errors.Is(err, ErrContainerNotFound) {
			return "", fmt.Errorf("%w: %s", ErrContainerNotFound, containerName)
		}
		return "", fmt.Errorf("failed to find container %s: %v", containerName, err)
	}

	// Get the logs from the runtime
	logs, err := d.runtime.GetWorkloadLogs(ctx, container.ID, follow)
	if err != nil {
		return "", fmt.Errorf("failed to get container logs %s: %v", containerName, err)
	}

	return logs, nil
}

func (d *defaultManager) findContainerByName(ctx context.Context, name string) (*rt.ContainerInfo, error) {
	// List containers to find the one with the given name
	containers, err := d.runtime.ListWorkloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %v", err)
	}

	// Find the container with the given name
	for _, c := range containers {
		// Check if the container is managed by ToolHive
		if !labels.IsToolHiveContainer(c.Labels) {
			continue
		}

		// Check if the container name matches
		containerName := labels.GetContainerName(c.Labels)
		if containerName == "" {
			name = c.Name // Fallback to container name
		}

		// Check if the name matches (exact match or prefix match)
		if containerName == name || c.ID == name {
			return &c, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrContainerNotFound, name)
}

func shouldRemoveClientConfig() bool {
	c := config.GetConfig()
	return len(c.Clients.RegisteredClients) > 0
}

// TODO: Move to dedicated config management interface.
// updateClientConfigurations updates client configuration files with the MCP server URL
func removeClientConfigurations(containerName string) error {
	// Find client configuration files
	configs, err := client.FindClientConfigs()
	if err != nil {
		return fmt.Errorf("failed to find client configurations: %w", err)
	}

	if len(configs) == 0 {
		logger.Info("No client configuration files found")
		return nil
	}

	for _, c := range configs {
		logger.Infof("Removing MCP server from client configuration: %s", c.Path)

		if err := c.ConfigUpdater.Remove(containerName); err != nil {
			logger.Warnf("Warning: Failed to remove MCP server from client configuration %s: %v", c.Path, err)
			continue
		}

		logger.Infof("Successfully removed MCP server from client configuration: %s", c.Path)
	}

	return nil
}

func isContainerRunning(container *rt.ContainerInfo) bool {
	return container.State == "running"
}

// loadRunnerFromState attempts to load a Runner from the state store
func (d *defaultManager) loadRunnerFromState(ctx context.Context, baseName string) (*runner.Runner, error) {
	// Load the runner from the state store
	r, err := runner.LoadState(ctx, baseName)
	if err != nil {
		return nil, err
	}

	// Update the runtime in the loaded configuration
	r.Config.Runtime = d.runtime

	return r, nil
}

func needSecretsPassword(secretOptions []string) bool {
	// If the user did not ask for any secrets, then don't attempt to instantiate
	// the secrets manager.
	if len(secretOptions) == 0 {
		return false
	}
	// Ignore err - if the flag is not set, it's not needed.
	providerType, _ := config.GetConfig().Secrets.GetProviderType()
	return providerType == secrets.EncryptedType
}

// cleanupTempPermissionProfile cleans up temporary permission profile files for a given base name
func (*defaultManager) cleanupTempPermissionProfile(ctx context.Context, baseName string) error {
	// Try to load the saved configuration to get the permission profile path
	r, err := runner.LoadState(ctx, baseName)
	if err != nil {
		// If we can't load the state, there's nothing to clean up
		logger.Debugf("Could not load state for %s, skipping permission profile cleanup: %v", baseName, err)
		return nil
	}

	// Clean up the temporary permission profile if it exists
	if r.Config.PermissionProfileNameOrPath != "" {
		if err := runner.CleanupTempPermissionProfile(r.Config.PermissionProfileNameOrPath); err != nil {
			return fmt.Errorf("failed to cleanup temporary permission profile: %v", err)
		}
	}

	return nil
}

// stopWorkloads stops the named workloads concurrently.
// It assumes that the workloads exist in the running state.
func (d *defaultManager) stopWorkloads(_ context.Context, workloads []*rt.ContainerInfo) *errgroup.Group {
	group := errgroup.Group{}
	for _, workload := range workloads {
		group.Go(func() error {
			childCtx, cancel := context.WithTimeout(context.Background(), AsyncOperationTimeout)
			defer cancel()

			name := labels.GetContainerBaseName(workload.Labels)
			// Stop the proxy process
			proxy.StopProcess(name)

			logger.Infof("Stopping containers for %s...", name)
			// Stop the container
			if err := d.runtime.StopWorkload(childCtx, workload.ID); err != nil {
				return fmt.Errorf("failed to stop container: %w", err)
			}

			if shouldRemoveClientConfig() {
				if err := removeClientConfigurations(name); err != nil {
					logger.Warnf("Warning: Failed to remove client configurations: %v", err)
				} else {
					logger.Infof("Client configurations for %s removed", name)
				}
			}

			logger.Infof("Successfully stopped %s...", name)
			return nil
		})
	}

	return &group
}

// DeleteWorkloads deletes the specified workloads by name.
func (d *defaultManager) DeleteWorkloads(_ context.Context, names []string) (*errgroup.Group, error) {
	// Validate all workload names to prevent path traversal attacks
	for _, name := range names {
		if err := validateWorkloadName(name); err != nil {
			return nil, fmt.Errorf("invalid workload name '%s': %w", name, err)
		}
	}

	group := &errgroup.Group{}

	for _, name := range names {
		group.Go(func() error {
			// Create a child context with a longer timeout
			childCtx, cancel := context.WithTimeout(context.Background(), AsyncOperationTimeout)
			defer cancel()

			// Find the container
			container, err := d.findContainerByName(childCtx, name)
			if err != nil {
				if errors.Is(err, ErrContainerNotFound) {
					// Log but don't fail the entire operation for not found containers
					logger.Warnf("Warning: Failed to delete workload %s: %v", name, err)
					return nil
				}
				return fmt.Errorf("failed to find workload %s: %v", name, err)
			}

			containerID := container.ID
			containerLabels := container.Labels
			baseName := labels.GetContainerBaseName(containerLabels)
			isRunning := isContainerRunning(container)

			if isRunning {
				// Stop the proxy process first (like StopWorkload does)
				logger.Infof("Removing proxy process for %s...", name)
				if baseName != "" {
					proxy.StopProcess(baseName)
				}
			}

			// Remove the container
			logger.Infof("Removing container %s...", name)
			if err := d.runtime.RemoveWorkload(childCtx, containerID); err != nil {
				return fmt.Errorf("failed to remove container: %v", err)
			}

			// Get the base name from the container labels
			if baseName != "" {
				// Clean up temporary permission profile before deleting saved state
				if err := d.cleanupTempPermissionProfile(childCtx, baseName); err != nil {
					logger.Warnf("Warning: Failed to cleanup temporary permission profile: %v", err)
				}

				// Delete the saved state if it exists
				if err := runner.DeleteSavedConfig(childCtx, baseName); err != nil {
					logger.Warnf("Warning: Failed to delete saved state: %v", err)
				} else {
					logger.Infof("Saved state for %s removed", baseName)
				}

				logger.Infof("Container %s removed", name)

				if shouldRemoveClientConfig() {
					if err := removeClientConfigurations(name); err != nil {
						logger.Warnf("Warning: Failed to remove client configurations: %v", err)
					} else {
						logger.Infof("Client configurations for %s removed", name)
					}
				}
			}

			return nil
		})
	}

	return group, nil
}

// RestartWorkloads restarts the specified workloads by name.
func (d *defaultManager) RestartWorkloads(_ context.Context, names []string) (*errgroup.Group, error) {
	// Validate all workload names to prevent path traversal attacks
	for _, name := range names {
		if err := validateWorkloadName(name); err != nil {
			return nil, fmt.Errorf("invalid workload name '%s': %w", name, err)
		}
	}

	group := &errgroup.Group{}

	for _, name := range names {
		group.Go(func() error {
			// Create a child context with a longer timeout
			childCtx, cancel := context.WithTimeout(context.Background(), AsyncOperationTimeout)
			defer cancel()

			var containerBaseName string
			var running bool
			// Try to find the container.
			container, err := d.findContainerByName(childCtx, name)
			if err != nil {
				if errors.Is(err, ErrContainerNotFound) {
					logger.Warnf("Warning: Failed to find container: %v", err)
					logger.Warnf("Trying to find state with name %s directly...", name)

					// Try to use the provided name as the base name
					containerBaseName = name
					running = false
				} else {
					return fmt.Errorf("failed to find workload %s: %v", name, err)
				}
			} else {
				// Container found, check if it's running and get the base name,
				running = isContainerRunning(container)
				containerBaseName = labels.GetContainerBaseName(container.Labels)
			}

			// Check if the proxy process is running
			proxyRunning := proxy.IsRunning(containerBaseName)

			if running && proxyRunning {
				logger.Infof("Container %s and proxy are already running", name)
				return nil
			}

			// Load the configuration from the state store
			// This is done synchronously since it is relatively inexpensive operation
			// and it allows for better error handling.
			mcpRunner, err := d.loadRunnerFromState(childCtx, containerBaseName)
			if err != nil {
				return fmt.Errorf("failed to load state for %s: %v", containerBaseName, err)
			}
			logger.Infof("Loaded configuration from state for %s", containerBaseName)

			// Run the tooling server inside a detached process.
			logger.Infof("Starting tooling server %s...", name)

			var containerID string
			if container != nil {
				containerID = container.ID
			}
			// If the container is running but the proxy is not, stop the container first
			if containerID != "" && running { // && !proxyRunning was previously here but is implied by previous if statement.
				logger.Infof("Container %s is running but proxy is not. Stopping container...", name)
				if err = d.runtime.StopWorkload(childCtx, containerID); err != nil {
					return fmt.Errorf("failed to stop container %s: %v", name, err)
				}
				logger.Infof("Container %s stopped", name)
			}

			return d.RunWorkloadDetached(mcpRunner.Config)
		})
	}

	return group, nil
}

func validateWorkloadName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: workload name cannot be empty", ErrInvalidWorkloadName)
	}

	// Use filepath.Clean to normalize the path
	cleanName := filepath.Clean(name)

	// Check if the cleaned path tries to escape current directory using filepath.Rel
	if rel, err := filepath.Rel(".", cleanName); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%w: workload name contains path traversal", ErrInvalidWorkloadName)
	}

	// Check for absolute paths
	if filepath.IsAbs(cleanName) {
		return fmt.Errorf("%w: workload name cannot be an absolute path", ErrInvalidWorkloadName)
	}

	// Check for command injection patterns (similar to permissions package)
	commandInjectionPattern := regexp.MustCompile(`[$&;|]|\$\(|\` + "`")
	if commandInjectionPattern.MatchString(name) {
		return fmt.Errorf("%w: workload name contains potentially dangerous characters", ErrInvalidWorkloadName)
	}

	// Check for null bytes
	if strings.Contains(name, "\x00") {
		return fmt.Errorf("%w: workload name contains null bytes", ErrInvalidWorkloadName)
	}

	// Validate against allowed pattern
	if !workloadNamePattern.MatchString(name) {
		return fmt.Errorf("%w: workload name can only contain alphanumeric characters, dots, hyphens, and underscores",
			ErrInvalidWorkloadName)
	}

	// Reasonable length limit
	if len(name) > 100 {
		return fmt.Errorf("%w: workload name too long (max 100 characters)", ErrInvalidWorkloadName)
	}

	return nil
}

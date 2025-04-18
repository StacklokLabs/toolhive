package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/StacklokLabs/toolhive/pkg/client"
	"github.com/StacklokLabs/toolhive/pkg/config"
	"github.com/StacklokLabs/toolhive/pkg/container"
	"github.com/StacklokLabs/toolhive/pkg/labels"
	"github.com/StacklokLabs/toolhive/pkg/logger"
	"github.com/StacklokLabs/toolhive/pkg/runner"
)

var rmCmd = &cobra.Command{
	Use:   "rm [container-name]",
	Short: "Remove an MCP server",
	Long:  `Remove an MCP server managed by ToolHive.`,
	Args:  cobra.ExactArgs(1),
	RunE:  rmCmdFunc,
}

var (
	rmForce bool
)

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Force removal of a running container")
}

//nolint:gocyclo // This function is complex but manageable
func rmCmdFunc(_ *cobra.Command, args []string) error {
	// Get container name
	containerName := args[0]

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create container runtime
	runtime, err := container.NewFactory().Create(ctx)
	if err != nil {
		return fmt.Errorf("failed to create container runtime: %v", err)
	}

	// List containers to find the one with the given name
	containers, err := runtime.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list containers: %v", err)
	}

	// Find the container with the given name
	var containerID string
	var isRunning bool
	var containerLabels map[string]string
	for _, c := range containers {
		// Check if the container is managed by ToolHive
		if !labels.IsToolHiveContainer(c.Labels) {
			continue
		}

		// Check if the container name matches
		name := labels.GetContainerName(c.Labels)
		if name == "" {
			name = c.Name // Fallback to container name
		}

		// Check if the name matches (exact match or prefix match)
		if name == containerName || strings.HasPrefix(c.ID, containerName) {
			containerID = c.ID
			isRunning = strings.Contains(strings.ToLower(c.State), "running")
			containerLabels = c.Labels
			break
		}
	}

	if containerID == "" {
		return fmt.Errorf("container %s not found", containerName)
	}

	// Check if the container is running and force is not specified
	if isRunning && !rmForce {
		return fmt.Errorf("container %s is running. Use -f to force remove", containerName)
	}

	// Remove the container
	logger.Log.Infof("Removing container %s...", containerName)
	if err := runtime.RemoveContainer(ctx, containerID); err != nil {
		return fmt.Errorf("failed to remove container: %v", err)
	}

	// Get the base name from the container labels
	baseName := labels.GetContainerBaseName(containerLabels)
	if baseName != "" {
		// Delete the saved state if it exists
		if err := runner.DeleteSavedConfig(ctx, baseName); err != nil {
			logger.Log.Warnf("Warning: Failed to delete saved state: %v", err)
		} else {
			logger.Log.Infof("Saved state for %s removed", baseName)
		}
	}

	logger.Log.Infof("Container %s removed", containerName)

	if shouldRemoveClientConfig() {
		if err := removeClientConfigurations(containerName); err != nil {
			logger.Log.Warnf("Warning: Failed to remove client configurations: %v", err)
		} else {
			logger.Log.Infof("Client configurations for %s removed", containerName)
		}
	}

	return nil
}

func shouldRemoveClientConfig() bool {
	c := config.GetConfig()
	return len(c.Clients.RegisteredClients) > 0 || c.Clients.AutoDiscovery
}

// updateClientConfigurations updates client configuration files with the MCP server URL
func removeClientConfigurations(containerName string) error {
	// Find client configuration files
	configs, err := client.FindClientConfigs()
	if err != nil {
		return fmt.Errorf("failed to find client configurations: %w", err)
	}

	if len(configs) == 0 {
		logger.Log.Infof("No client configuration files found")
		return nil
	}

	for _, c := range configs {
		logger.Log.Infof("Removing MCP server from client configuration: %s", c.Path)

		if err := c.ConfigUpdater.Remove(containerName); err != nil {
			logger.Log.Warnf("Warning: Failed to remove MCP server from client configurationn %s: %v", c.Path, err)
			continue
		}

		logger.Log.Infof("Successfully removed MCP server from client configuration: %s", c.Path)
	}

	return nil
}

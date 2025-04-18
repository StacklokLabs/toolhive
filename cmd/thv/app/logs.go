package app

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/StacklokLabs/toolhive/pkg/container"
	"github.com/StacklokLabs/toolhive/pkg/labels"
	"github.com/StacklokLabs/toolhive/pkg/logger"
)

func newLogsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logs [container-name]",
		Short: "Output the logs of an MCP server",
		Long:  `Output the logs of an MCP server managed by Vibe Tool.`,
		Args:  cobra.ExactArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			// Get container name
			containerName := args[0]

			// Create context
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Create container runtime
			runtime, err := container.NewFactory().Create(ctx)
			if err != nil {
				logger.Log.Errorf("failed to create container runtime: %v", err)
				return
			}

			// List containers to find the one with the given name
			containers, err := runtime.ListContainers(ctx)
			if err != nil {
				logger.Log.Errorf("failed to list containers: %v", err)
				return
			}

			// Find the container with the given name
			var containerID string
			for _, c := range containers {
				// Check if the container is managed by Vibe Tool
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
					break
				}
			}

			if containerID == "" {
				logger.Log.Infof("container %s not found", containerName)
				return
			}

			logs, err := runtime.ContainerLogs(ctx, containerID)
			if err != nil {
				logger.Log.Errorf("failed to get container logs: %v", err)
				return
			}
			logger.Log.Infof(logs)

		},
	}
}

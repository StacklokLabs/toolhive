package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/workloads"
)

var (
	followFlag bool
)

func logsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs [container-name|prune]",
		Short: "Output the logs of an MCP server or manage log files",
		Long:  `Output the logs of an MCP server managed by ToolHive, or manage log files.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if the argument is "prune"
			if args[0] == "prune" {
				return logsPruneCmdFunc(cmd)
			}
			return logsCmdFunc(cmd, args)
		},
		ValidArgsFunction: completeLogsArgs,
	}

	logsCommand.Flags().BoolVarP(&followFlag, "follow", "f", false, "Follow log output (only for container logs)")
	err := viper.BindPFlag("follow", logsCommand.Flags().Lookup("follow"))
	if err != nil {
		logger.Errorf("failed to bind flag: %v", err)
	}

	// Add prune subcommand for better discoverability
	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete log files from servers not currently managed by ToolHive",
		Long: `Delete log files from servers that are not currently managed by ToolHive (running or stopped).
This helps clean up old log files that accumulate over time from removed servers.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return logsPruneCmdFunc(cmd)
		},
	}
	logsCommand.AddCommand(pruneCmd)

	return logsCommand
}

func logsCmdFunc(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// Get container name
	containerName := args[0]
	follow := viper.GetBool("follow")

	manager, err := workloads.NewManager(ctx)
	if err != nil {
		return fmt.Errorf("failed to create lifecycle manager: %v", err)
	}

	logs, err := manager.GetLogs(ctx, containerName, follow)
	if err != nil {
		if errors.Is(err, workloads.ErrContainerNotFound) {
			logger.Infof("container %s not found", containerName)
			return nil
		}
		return fmt.Errorf("failed to get logs for container %s: %v", containerName, err)
	}

	fmt.Print(logs)
	return nil
}

func logsPruneCmdFunc(cmd *cobra.Command) error {
	ctx := cmd.Context()

	logsDir, err := getLogsDirectory()
	if err != nil {
		return err
	}

	managedNames, err := getManagedContainerNames(ctx)
	if err != nil {
		return err
	}

	logFiles, err := getLogFiles(logsDir)
	if err != nil {
		return err
	}

	if len(logFiles) == 0 {
		logger.Info("No log files found")
		return nil
	}

	prunedFiles, errs := pruneOrphanedLogFiles(logFiles, managedNames)
	reportPruneResults(prunedFiles, errs)

	return nil
}

func getLogsDirectory() (string, error) {
	logsDir, err := xdg.DataFile("toolhive/logs")
	if err != nil {
		return "", fmt.Errorf("failed to get logs directory path: %v", err)
	}

	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		logger.Info("No logs directory found, nothing to prune")
		return "", nil
	}

	return logsDir, nil
}

func getManagedContainerNames(ctx context.Context) (map[string]bool, error) {
	manager, err := workloads.NewManager(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create container manager: %v", err)
	}

	managedContainers, err := manager.ListWorkloads(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %v", err)
	}

	managedNames := make(map[string]bool)
	for _, c := range managedContainers {
		name := c.Name
		if name != "" {
			managedNames[name] = true
		}
	}

	return managedNames, nil
}

func getLogFiles(logsDir string) ([]string, error) {
	if logsDir == "" {
		return nil, nil
	}

	logFiles, err := filepath.Glob(filepath.Join(logsDir, "*.log"))
	if err != nil {
		return nil, fmt.Errorf("failed to list log files: %v", err)
	}

	return logFiles, nil
}

func pruneOrphanedLogFiles(logFiles []string, managedNames map[string]bool) ([]string, []string) {
	var prunedFiles []string
	var errs []string

	for _, logFile := range logFiles {
		baseName := strings.TrimSuffix(filepath.Base(logFile), ".log")

		if !managedNames[baseName] {
			if err := os.Remove(logFile); err != nil {
				errs = append(errs, fmt.Sprintf("failed to remove %s: %v", logFile, err))
				logger.Warnf("Failed to remove log file %s: %v", logFile, err)
			} else {
				prunedFiles = append(prunedFiles, logFile)
				logger.Infof("Removed log file: %s", logFile)
			}
		}
	}

	return prunedFiles, errs
}

func reportPruneResults(prunedFiles, errs []string) {
	if len(prunedFiles) == 0 {
		logger.Info("No orphaned log files found to prune")
	} else {
		logger.Infof("Successfully pruned %d log file(s)", len(prunedFiles))
		for _, file := range prunedFiles {
			fmt.Printf("Removed: %s\n", file)
		}
	}

	if len(errs) > 0 {
		logger.Warnf("Encountered %d error(s) during pruning:", len(errs))
		for _, errMsg := range errs {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
		}
	}
}

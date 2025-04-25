// Package app provides the entry point for the toolhive command-line application.
package app

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/StacklokLabs/toolhive/pkg/logger"
	"github.com/StacklokLabs/toolhive/pkg/updates"
)

var rootCmd = &cobra.Command{
	Use:               "thv",
	DisableAutoGenTag: true,
	Short:             "ToolHive (thv) is a lightweight, secure, and fast manager for MCP servers",
	Long: `ToolHive (thv) is a lightweight, secure, and fast manager for MCP (Model Context Protocol) servers.
It is written in Go and has extensive test coverage—including input validation—to ensure reliability and security.

Under the hood, ToolHive acts as a very thin client for the Docker/Podman Unix socket API.
This design choice allows it to remain both efficient and lightweight while still providing powerful,
container-based isolation for running MCP servers.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// If no subcommand is provided, print help
		if err := cmd.Help(); err != nil {
			logger.Log.Errorf("Error displaying help: %v", err)
		}
	},
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		logger.Initialize()
	},
}

// NewRootCmd creates a new root command for the ToolHive CLI.
func NewRootCmd() *cobra.Command {
	// Add persistent flags
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")
	err := viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
	if err != nil {
		logger.Log.Errorf("Error binding debug flag: %v", err)
	}

	// Add subcommands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(proxyCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(logsCommand())
	rootCmd.AddCommand(newSecretCommand())

	// Skip update check for completion command
	if !IsCompletionCommand(os.Args) {
		checkForUpdates()
	}

	return rootCmd
}

// IsCompletionCommand checks if the command being run is the completion command
func IsCompletionCommand(args []string) bool {
	if len(args) > 1 {
		return args[1] == "completion"
	}
	return false
}

func checkForUpdates() {
	versionClient := updates.NewVersionClient()
	updateChecker, err := updates.NewUpdateChecker(versionClient)
	// treat update-related errors as non-fatal
	if err != nil {
		logger.Log.Errorf("unable to create update client: %w", err)
		return
	}

	err = updateChecker.CheckLatestVersion()
	if err != nil {
		logger.Log.Errorf("error while checking for updates: %w", err)
	}
}

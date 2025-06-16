// Package main is the entry point for the ToolHive CLI.
package main

import (
	"os"

	"github.com/stacklok/toolhive/cmd/thv/app"
	"github.com/stacklok/toolhive/pkg/container"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/versions"
)

func main() {
	// Initialize the logger
	logger.Initialize()

	// Skip update check for completion command, if we are running in kubernetes, or if BuildType is not "release"
	enableUpdates := !app.IsCompletionCommand(os.Args) && !container.IsKubernetesRuntime() && versions.BuildType == "release"
	if err := app.NewRootCmd(enableUpdates).Execute(); err != nil {
		os.Exit(1)
	}
}

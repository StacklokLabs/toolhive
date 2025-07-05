// Package runner provides functionality for running MCP servers
package runner

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/client"
	"github.com/stacklok/toolhive/pkg/kubernetes/config"
	"github.com/stacklok/toolhive/pkg/kubernetes/labels"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/mcp"
	"github.com/stacklok/toolhive/pkg/kubernetes/process"
	"github.com/stacklok/toolhive/pkg/kubernetes/secrets"
	"github.com/stacklok/toolhive/pkg/kubernetes/telemetry"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport/types"
)

// Runner is responsible for running an MCP server with the provided configuration
type Runner struct {
	// Config is the configuration for the runner
	Config *RunConfig

	// telemetryProvider is the OpenTelemetry provider for cleanup
	telemetryProvider *telemetry.Provider
}

// NewRunner creates a new Runner with the provided configuration
func NewRunner(runConfig *RunConfig) *Runner {
	return &Runner{
		Config: runConfig,
	}
}

// Run runs the MCP server with the provided configuration
//
//nolint:gocyclo // This function is complex but manageable
func (r *Runner) Run(ctx context.Context) error {
	// Create transport with runtime
	transportConfig := types.Config{
		Type:       r.Config.Transport,
		Port:       r.Config.Port,
		TargetPort: r.Config.TargetPort,
		Host:       r.Config.Host,
		TargetHost: r.Config.TargetHost,
		Runtime:    r.Config.Runtime,
		Debug:      r.Config.Debug,
	}

	// Get authentication middleware
	allowOpaqueTokens := false
	if r.Config.OIDCConfig != nil && r.Config.OIDCConfig.AllowOpaqueTokens {
		allowOpaqueTokens = r.Config.OIDCConfig.AllowOpaqueTokens
	}
	authMiddleware, err := auth.GetAuthenticationMiddleware(ctx, r.Config.OIDCConfig, allowOpaqueTokens)
	if err != nil {
		return fmt.Errorf("failed to create authentication middleware: %v", err)
	}
	transportConfig.Middlewares = append(transportConfig.Middlewares, authMiddleware)

	// Add MCP parsing middleware after authentication
	logger.Info("MCP parsing middleware enabled for transport")
	transportConfig.Middlewares = append(transportConfig.Middlewares, mcp.ParsingMiddleware)

	// Add telemetry middleware if telemetry configuration is provided
	if r.Config.TelemetryConfig != nil {
		logger.Info("OpenTelemetry instrumentation enabled for transport")

		// Create telemetry provider
		telemetryProvider, err := telemetry.NewProvider(ctx, *r.Config.TelemetryConfig)
		if err != nil {
			return fmt.Errorf("failed to create telemetry provider: %w", err)
		}

		// Create telemetry middleware with server name and transport type
		telemetryMiddleware := telemetryProvider.Middleware(r.Config.Name, r.Config.Transport.String())
		transportConfig.Middlewares = append(transportConfig.Middlewares, telemetryMiddleware)

		// Add Prometheus handler to transport config if metrics port is configured
		if r.Config.TelemetryConfig.EnablePrometheusMetricsPath {
			transportConfig.PrometheusHandler = telemetryProvider.PrometheusHandler()
			logger.Infof("Prometheus metrics will be exposed on port %d at /metrics", r.Config.Port)
		}

		// Store provider for cleanup
		r.telemetryProvider = telemetryProvider
	}

	// Add authorization middleware if authorization configuration is provided
	if r.Config.AuthzConfig != nil {
		logger.Info("Authorization enabled for transport")

		// Get the middleware from the configuration
		middleware, err := r.Config.AuthzConfig.CreateMiddleware()
		if err != nil {
			return fmt.Errorf("failed to get authorization middleware: %v", err)
		}

		// Add authorization middleware to transport config
		transportConfig.Middlewares = append(transportConfig.Middlewares, middleware)
	}

	// Add audit middleware if audit configuration is provided
	if r.Config.AuditConfig != nil {
		logger.Info("Audit logging enabled for transport")

		// Set the component name if not already set
		if r.Config.AuditConfig.Component == "" {
			r.Config.AuditConfig.Component = r.Config.ContainerName
		}

		// Get the middleware from the configuration
		middleware, err := r.Config.AuditConfig.CreateMiddleware()
		if err != nil {
			return fmt.Errorf("failed to create audit middleware: %w", err)
		}

		// Add audit middleware to transport config
		transportConfig.Middlewares = append(transportConfig.Middlewares, middleware)
	}

	transportHandler, err := transport.NewFactory().Create(transportConfig)
	if err != nil {
		return fmt.Errorf("failed to create transport: %v", err)
	}

	// Save the configuration to the state store
	if err := r.SaveState(ctx); err != nil {
		logger.Warnf("Warning: Failed to save run configuration: %v", err)
	}

	// Process secrets if provided
	// NOTE: This MUST happen after we save the run config to avoid storing
	// the secrets in the state store.
	if len(r.Config.Secrets) > 0 {
		cfg := config.GetConfig()

		providerType, err := cfg.Secrets.GetProviderType()
		if err != nil {
			return fmt.Errorf("error determining secrets provider type: %w", err)
		}

		secretManager, err := secrets.CreateSecretProvider(providerType)
		if err != nil {
			return fmt.Errorf("error instantiating secret manager %v", err)
		}

		// Process secrets
		if _, err = r.Config.WithSecrets(ctx, secretManager); err != nil {
			return err
		}
	}

	// Set up the transport
	logger.Infof("Setting up %s transport...", r.Config.Transport)
	if err := transportHandler.Setup(
		ctx, r.Config.Runtime, r.Config.ContainerName, r.Config.Image, r.Config.CmdArgs,
		r.Config.EnvVars, r.Config.ContainerLabels, r.Config.PermissionProfile, r.Config.K8sPodTemplatePatch,
		r.Config.IsolateNetwork,
	); err != nil {
		return fmt.Errorf("failed to set up transport: %v", err)
	}

	// Start the transport (which also starts the container and monitoring)
	logger.Infof("Starting %s transport for %s...", r.Config.Transport, r.Config.ContainerName)
	if err := transportHandler.Start(ctx); err != nil {
		return fmt.Errorf("failed to start transport: %v", err)
	}

	logger.Infof("MCP server %s started successfully", r.Config.ContainerName)

	// Update client configurations with the MCP server URL.
	// Note that this function checks the configuration to determine which
	// clients should be updated, if any.
	if err := updateClientConfigurations(r.Config.ContainerName, r.Config.ContainerLabels, "localhost", r.Config.Port); err != nil {
		logger.Warnf("Warning: Failed to update client configurations: %v", err)
	}

	// Define a function to stop the MCP server
	stopMCPServer := func(reason string) {
		logger.Infof("Stopping MCP server: %s", reason)

		// Stop the transport (which also stops the container, monitoring, and handles removal)
		logger.Infof("Stopping %s transport...", r.Config.Transport)
		if err := transportHandler.Stop(ctx); err != nil {
			logger.Warnf("Warning: Failed to stop transport: %v", err)
		}

		// Cleanup telemetry provider
		if err := r.Cleanup(ctx); err != nil {
			logger.Warnf("Warning: Failed to cleanup telemetry: %v", err)
		}

		// Remove the PID file if it exists
		if err := process.RemovePIDFile(r.Config.BaseName); err != nil {
			logger.Warnf("Warning: Failed to remove PID file: %v", err)
		}

		logger.Infof("MCP server %s stopped", r.Config.ContainerName)
	}

	if process.IsDetached() {
		// We're a detached process running in foreground mode
		// Write the PID to a file so the stop command can kill the process
		if err := process.WriteCurrentPIDFile(r.Config.BaseName); err != nil {
			logger.Warnf("Warning: Failed to write PID file: %v", err)
		}

		logger.Infof("Running as detached process (PID: %d)", os.Getpid())
	} else {
		logger.Info("Press Ctrl+C to stop or wait for container to exit")
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Create a done channel to signal when the server has been stopped
	doneCh := make(chan struct{})

	// Start a goroutine to monitor the transport's running state
	go func() {
		for {
			// Safely check if transportHandler is nil
			if transportHandler == nil {
				logger.Info("Transport handler is nil, exiting monitoring routine...")
				close(doneCh)
				return
			}

			// Check if the transport is still running
			running, err := transportHandler.IsRunning(ctx)
			if err != nil {
				logger.Errorf("Error checking transport status: %v", err)
				// Don't exit immediately on error, try again after pause
				time.Sleep(1 * time.Second)
				continue
			}
			if !running {
				// Transport is no longer running (container exited or was stopped)
				logger.Info("Transport is no longer running, exiting...")
				close(doneCh)
				return
			}

			// Sleep for a short time before checking again
			time.Sleep(1 * time.Second)
		}
	}()

	// Wait for either a signal or the done channel to be closed
	select {
	case sig := <-sigCh:
		stopMCPServer(fmt.Sprintf("Received signal %s", sig))
	case <-doneCh:
		// The transport has already been stopped (likely by the container monitor)
		// Clean up the PID file and state
		if err := process.RemovePIDFile(r.Config.BaseName); err != nil {
			logger.Warnf("Warning: Failed to remove PID file: %v", err)
		}

		logger.Infof("MCP server %s stopped", r.Config.ContainerName)
	}

	return nil
}

// Cleanup performs cleanup operations for the runner, including shutting down telemetry.
func (r *Runner) Cleanup(ctx context.Context) error {
	if r.telemetryProvider != nil {
		logger.Debug("Shutting down telemetry provider")
		if err := r.telemetryProvider.Shutdown(ctx); err != nil {
			logger.Warnf("Warning: Failed to shutdown telemetry provider: %v", err)
			return err
		}
	}
	return nil
}

// updateClientConfigurations updates client configuration files with the MCP server URL
func updateClientConfigurations(containerName string, containerLabels map[string]string, host string, port int) error {
	// Find client configuration files
	clientConfigs, err := client.FindClientConfigs()
	if err != nil {
		return fmt.Errorf("failed to find client configurations: %w", err)
	}

	if len(clientConfigs) == 0 {
		logger.Infof("No client configuration files found")
		return nil
	}

	// Generate the URL for the MCP server
	transportType := labels.GetTransportType(containerLabels)
	url := client.GenerateMCPServerURL(transportType, host, port, containerName)

	// Update each configuration file
	for _, clientConfig := range clientConfigs {
		logger.Infof("Updating client configuration: %s", clientConfig.Path)

		if err := client.Upsert(clientConfig, containerName, url, transportType); err != nil {
			fmt.Printf("Warning: Failed to update MCP server configuration in %s: %v\n", clientConfig.Path, err)
			continue
		}

		logger.Infof("Successfully updated client configuration: %s", clientConfig.Path)
	}

	return nil
}

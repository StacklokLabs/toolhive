package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stacklok/toolhive/pkg/config"
	"github.com/stacklok/toolhive/pkg/container"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/permissions"
	"github.com/stacklok/toolhive/pkg/process"
	"github.com/stacklok/toolhive/pkg/registry"
	"github.com/stacklok/toolhive/pkg/runner"
	"github.com/stacklok/toolhive/pkg/runner/retriever"
	"github.com/stacklok/toolhive/pkg/transport"
	"github.com/stacklok/toolhive/pkg/workloads"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] [SERVER_OR_IMAGE_OR_PROTOCOL [-- ARGS...]]",
	Short: "Run an MCP server",
	Long: `Run an MCP server with the specified name, image, or protocol scheme.

ToolHive supports three ways to run an MCP server:

1. From the registry:
   $ thv run server-name [-- args...]
   Looks up the server in the registry and uses its predefined settings
   (transport, permissions, environment variables, etc.)

2. From a container image:
   $ thv run ghcr.io/example/mcp-server:latest [-- args...]
   Runs the specified container image directly with the provided arguments

3. Using a protocol scheme:
   $ thv run uvx://package-name [-- args...]
   $ thv run npx://package-name [-- args...]
   $ thv run go://package-name [-- args...]
   $ thv run go://./local-path [-- args...]
   Automatically generates a container that runs the specified package
   using either uvx (Python with uv package manager), npx (Node.js),
   or go (Golang). For Go, you can also specify local paths starting
   with './' or '../' to build and run local Go projects.

If no server is specified, ToolHive will run all servers configured in the
default_servers list in the config file.

The container will be started with the specified transport mode and
permission profile. Additional configuration can be provided via flags.`,
	Args: cobra.ArbitraryArgs,
	RunE: runCmdFunc,
	// Ignore unknown flags to allow passing flags to the MCP server
	FParseErrWhitelist: cobra.FParseErrWhitelist{
		UnknownFlags: true,
	},
}

var (
	runTransport         string
	runName              string
	runHost              string
	runPort              int
	runTargetPort        int
	runTargetHost        string
	runPermissionProfile string
	runEnv               []string
	runForeground        bool
	runVolumes           []string
	runSecrets           []string
	runAuthzConfig       string
	runAuditConfig       string
	runEnableAudit       bool
	runK8sPodPatch       string
	runCACertPath        string
	runVerifyImage       string
	runSaveConfig        bool

	// OpenTelemetry flags
	runOtelEndpoint                    string
	runOtelServiceName                 string
	runOtelSamplingRate                float64
	runOtelHeaders                     []string
	runOtelInsecure                    bool
	runOtelEnablePrometheusMetricsPath bool
	runOtelEnvironmentVariables        []string

	// Network isolation flag
	runIsolateNetwork bool
)

func init() {
	runCmd.Flags().StringVar(&runTransport, "transport", "stdio", "Transport mode (sse, streamable-http or stdio)")
	runCmd.Flags().StringVar(&runName, "name", "", "Name of the MCP server (auto-generated from image if not provided)")
	runCmd.Flags().StringVar(&runHost, "host", transport.LocalhostIPv4, "Host for the HTTP proxy to listen on (IP or hostname)")
	runCmd.Flags().IntVar(&runPort, "port", 0, "Port for the HTTP proxy to listen on (host port)")
	runCmd.Flags().IntVar(&runTargetPort, "target-port", 0,
		"Port for the container to expose (only applicable to SSE or Streamable HTTP transport)")
	runCmd.Flags().StringVar(
		&runTargetHost,
		"target-host",
		transport.LocalhostIPv4,
		"Host to forward traffic to (only applicable to SSE or Streamable HTTP transport)")
	runCmd.Flags().StringVar(
		&runPermissionProfile,
		"permission-profile",
		permissions.ProfileNetwork,
		"Permission profile to use (none, network, or path to JSON file)",
	)
	runCmd.Flags().StringArrayVarP(
		&runEnv,
		"env",
		"e",
		[]string{},
		"Environment variables to pass to the MCP server (format: KEY=VALUE)",
	)
	runCmd.Flags().BoolVarP(&runForeground, "foreground", "f", false, "Run in foreground mode (block until container exits)")
	runCmd.Flags().StringArrayVarP(
		&runVolumes,
		"volume",
		"v",
		[]string{},
		"Mount a volume into the container (format: host-path:container-path[:ro])",
	)
	runCmd.Flags().StringArrayVar(
		&runSecrets,
		"secret",
		[]string{},
		"Specify a secret to be fetched from the secrets manager and set as an environment variable (format: NAME,target=TARGET)",
	)
	runCmd.Flags().StringVar(
		&runAuthzConfig,
		"authz-config",
		"",
		"Path to the authorization configuration file",
	)
	runCmd.Flags().StringVar(
		&runAuditConfig,
		"audit-config",
		"",
		"Path to the audit configuration file",
	)
	runCmd.Flags().BoolVar(
		&runEnableAudit,
		"enable-audit",
		false,
		"Enable audit logging with default configuration",
	)
	runCmd.Flags().StringVar(
		&runK8sPodPatch,
		"k8s-pod-patch",
		"",
		"JSON string to patch the Kubernetes pod template (only applicable when using Kubernetes runtime)",
	)
	runCmd.Flags().StringVar(
		&runCACertPath,
		"ca-cert",
		"",
		"Path to a custom CA certificate file to use for container builds",
	)
	runCmd.Flags().StringVar(
		&runVerifyImage,
		"image-verification",
		retriever.VerifyImageWarn,
		fmt.Sprintf(
			"Set image verification mode (%s, %s, %s)",
			retriever.VerifyImageWarn,
			retriever.VerifyImageEnabled,
			retriever.VerifyImageDisabled,
		),
	)
	runCmd.Flags().BoolVarP(&runSaveConfig, "save-config", "s", false,
		"Save the provided command line arguments to the config file for this server")

	// This is used for the K8s operator which wraps the run command, but shouldn't be visible to users.
	if err := runCmd.Flags().MarkHidden("k8s-pod-patch"); err != nil {
		logger.Warnf("Error hiding flag: %v", err)
	}

	// Add OIDC validation flags
	AddOIDCFlags(runCmd)

	// Add OpenTelemetry flags
	runCmd.Flags().StringVar(&runOtelEndpoint, "otel-endpoint", "",
		"OpenTelemetry OTLP endpoint URL (e.g., https://api.honeycomb.io)")
	runCmd.Flags().StringVar(&runOtelServiceName, "otel-service-name", "",
		"OpenTelemetry service name (defaults to toolhive-mcp-proxy)")
	runCmd.Flags().Float64Var(&runOtelSamplingRate, "otel-sampling-rate", 0.1,
		"OpenTelemetry trace sampling rate (0.0-1.0)")
	runCmd.Flags().StringArrayVar(&runOtelHeaders, "otel-headers", nil,
		"OpenTelemetry OTLP headers in key=value format (e.g., x-honeycomb-team=your-api-key)")
	runCmd.Flags().BoolVar(&runOtelInsecure, "otel-insecure", false,
		"Disable TLS verification for OpenTelemetry endpoint")
	runCmd.Flags().BoolVar(&runOtelEnablePrometheusMetricsPath, "otel-enable-prometheus-metrics-path", false,
		"Enable Prometheus-style /metrics endpoint on the main transport port")
	runCmd.Flags().StringArrayVar(&runOtelEnvironmentVariables, "otel-env-vars", nil,
		"Environment variable names to include in OpenTelemetry spans "+
			"(comma-separated: ENV1,ENV2)")
	runCmd.Flags().BoolVar(&runIsolateNetwork, "isolate-network", false,
		"Isolate the container network from the host (default: false)")

}

func runCmdFunc(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Validate the host flag and default resolving to IP in case hostname is provided
	validatedHost, err := ValidateAndNormaliseHostFlag(runHost)
	if err != nil {
		return fmt.Errorf("invalid host: %s", runHost)
	}
	runHost = validatedHost

	// If no server is specified, run all default servers
	if len(args) == 0 {
		return runDefaultServers(ctx, cmd)
	}

	return runServer(ctx, cmd, args)
}

// runDefaultServers runs all default servers
func runDefaultServers(ctx context.Context, cmd *cobra.Command) error {
	cfg := config.GetConfig()
	if len(cfg.DefaultServers) == 0 {
		return fmt.Errorf("no default servers configured")
	}

	// Run each default server
	for _, server := range cfg.DefaultServers {
		if err := runServer(ctx, cmd, []string{server}); err != nil {
			logger.Warnf("Failed to run default server %s: %v", server, err)
		}
	}
	return nil
}

// runServer runs a single MCP server with the given arguments
func runServer(ctx context.Context, cmd *cobra.Command, args []string) error {
	// Get the name of the MCP server to run.
	// This may be a server name from the registry, a container image, or a protocol scheme.
	serverOrImage := args[0]

	// Process command arguments using os.Args to find everything after --
	cmdArgs := parseCommandArguments(os.Args)
	mergedArgs := resolveCommandAndConfigArgs(cmdArgs, serverOrImage)

	// Print the processed command arguments for debugging
	logger.Debugf("Processed cmdArgs: %v", cmdArgs)

	// Get debug mode flag
	debugMode, _ := cmd.Flags().GetBool("debug")

	// Get OIDC flag values
	oidcIssuer := GetStringFlagOrEmpty(cmd, "oidc-issuer")
	oidcAudience := GetStringFlagOrEmpty(cmd, "oidc-audience")
	oidcJwksURL := GetStringFlagOrEmpty(cmd, "oidc-jwks-url")
	oidcClientID := GetStringFlagOrEmpty(cmd, "oidc-client-id")

	// Get OTEL flag values with config fallbacks
	cfg := config.GetConfig()

	// Use config values as fallbacks for OTEL flags if not explicitly set
	finalOtelEndpoint := runOtelEndpoint
	if !cmd.Flags().Changed("otel-endpoint") && cfg.OTEL.Endpoint != "" {
		finalOtelEndpoint = cfg.OTEL.Endpoint
	}

	finalOtelSamplingRate := runOtelSamplingRate
	if !cmd.Flags().Changed("otel-sampling-rate") && cfg.OTEL.SamplingRate != 0.0 {
		finalOtelSamplingRate = cfg.OTEL.SamplingRate
	}

	finalOtelEnvironmentVariables := runOtelEnvironmentVariables
	if !cmd.Flags().Changed("otel-env-vars") && len(cfg.OTEL.EnvVars) > 0 {
		finalOtelEnvironmentVariables = cfg.OTEL.EnvVars
	}

	// Create container runtime
	rt, err := container.NewFactory().Create(ctx)
	if err != nil {
		return fmt.Errorf("failed to create container runtime: %v", err)
	}
	workloadManager := workloads.NewManagerFromRuntime(rt)

	// Select an env var validation strategy depending on how the CLI is run:
	// If we have called the CLI directly, we use the CLIEnvVarValidator.
	// If we are running in detached mode, or the CLI is wrapped by the K8s operator,
	// we use the DetachedEnvVarValidator.
	var envVarValidator runner.EnvVarValidator
	if process.IsDetached() || container.IsKubernetesRuntime() {
		envVarValidator = &runner.DetachedEnvVarValidator{}
	} else {
		envVarValidator = &runner.CLIEnvVarValidator{}
	}

	var imageMetadata *registry.ImageMetadata
	imageURL := serverOrImage

	// Only pull image if we are not running in Kubernetes mode.
	// This split will go away if we implement a separate command or binary
	// for running MCP servers in Kubernetes.
	if !container.IsKubernetesRuntime() {
		// Take the MCP server we were supplied and either fetch the image, or
		// build it from a protocol scheme. If the server URI refers to an image
		// in our trusted registry, we will also fetch the image metadata.
		imageURL, imageMetadata, err = retriever.GetMCPServer(ctx, serverOrImage, runCACertPath, runVerifyImage)
		if err != nil {
			return fmt.Errorf("failed to find or create the MCP server %s: %v", serverOrImage, err)
		}
	}

	// Initialize a new RunConfig with values from command-line flags
	// TODO: As noted elsewhere, we should use the builder pattern here to make it more readable.
	runConfig, err := runner.NewRunConfigFromFlags(
		ctx,
		rt,
		argsMapToSlice(mergedArgs),
		runName,
		imageURL,
		imageMetadata,
		runHost,
		debugMode,
		runVolumes,
		runSecrets,
		runAuthzConfig,
		runAuditConfig,
		runEnableAudit,
		runPermissionProfile,
		runTargetHost,
		runTransport,
		runPort,
		runTargetPort,
		runEnv,
		oidcIssuer,
		oidcAudience,
		oidcJwksURL,
		oidcClientID,
		finalOtelEndpoint,
		runOtelServiceName,
		finalOtelSamplingRate,
		runOtelHeaders,
		runOtelInsecure,
		runOtelEnablePrometheusMetricsPath,
		finalOtelEnvironmentVariables,
		runIsolateNetwork,
		runK8sPodPatch,
		envVarValidator,
	)
	if err != nil {
		return fmt.Errorf("failed to create RunConfig: %v", err)
	}

	// Once we have built the RunConfig, start the MCP workload.
	// If we are running the container in the foreground - call the RunWorkload method directly.
	if runForeground {
		return workloadManager.RunWorkload(ctx, runConfig)
	}
	return workloadManager.RunWorkloadDetached(runConfig)
}

// parseCommandArguments processes command-line arguments to find everything after the -- separator
// which are the arguments to be passed to the MCP server
func parseCommandArguments(args []string) []string {
	var cmdArgs []string
	for i, arg := range args {
		if arg == "--" && i < len(args)-1 {
			// Found the separator, take everything after it
			cmdArgs = args[i+1:]
			break
		}
	}
	return cmdArgs
}

// parseArgsToMap converts a slice of command line arguments to a map[string]string
// It handles both short flags (-f) and long flags (--flag) with their values
func parseArgsToMap(args []string) map[string]string {
	result := make(map[string]string)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Skip empty arguments
		if arg == "" {
			continue
		}

		// Handle long flags (--flag=value or --flag value)
		if strings.HasPrefix(arg, "--") {
			// Check if it's --flag=value format
			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg, "=", 2)
				flag := strings.TrimPrefix(parts[0], "--")
				result[flag] = parts[1]
			} else {
				// Check if next argument is a value (not a flag)
				flag := strings.TrimPrefix(arg, "--")
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					result[flag] = args[i+1]
					i++ // Skip the value in next iteration
				} else {
					// Flag without value, set to empty string
					result[flag] = ""
				}
			}
			continue
		}

		// Handle short flags (-f=value, -f value, or -f)
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			// Check if it's -f=value format
			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg, "=", 2)
				flag := strings.TrimPrefix(parts[0], "-")
				result[flag] = parts[1]
			} else {
				// Check if next argument is a value (not a flag)
				flag := strings.TrimPrefix(arg, "-")
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					result[flag] = args[i+1]
					i++ // Skip the value in next iteration
				} else {
					// Flag without value, set to empty string
					result[flag] = ""
				}
			}
			continue
		}

		// If it's not a flag, treat as positional argument
		// You might want to handle this differently based on your needs
		result[fmt.Sprintf("arg_%d", len(result))] = arg
	}

	return result
}

func resolveCommandAndConfigArgs(cmdArgs []string, serverOrImage string) map[string]string {
	cmdArgsMap := parseArgsToMap(cmdArgs)

	var mergedArgs map[string]string

	// Get any configured arguments for this server
	cfg, err := config.LoadOrCreateConfig()
	if err != nil {
		logger.Warnf("Failed to load config: %v", err)
		mergedArgs = cmdArgsMap
	} else if cfg != nil {
		// Get global arguments first
		globalArgs := cfg.GetGlobalServerArgs()
		logger.Debugf("Found global arguments: %v", globalArgs)

		// Get server-specific arguments
		serverArgs := cfg.GetServerArgs(serverOrImage)
		logger.Debugf("Found server-specific arguments: %v", serverArgs)

		// If save-config flag is set, save the command line arguments to config
		// Do this before merging to only save explicitly provided arguments
		if runSaveConfig && len(cmdArgsMap) > 0 {
			if err := cfg.SetServerArgs(serverOrImage, cmdArgsMap); err != nil {
				logger.Warnf("Failed to save arguments to config: %v", err)
			} else {
				logger.Infof("Saved arguments to config for server %s: %v", serverOrImage, cmdArgsMap)
			}
		}

		// Merge config args with command line args for this run
		if len(serverArgs) > 0 || len(globalArgs) > 0 {
			mergedArgs = mergeArguments(globalArgs, serverArgs, cmdArgsMap)
			logger.Debugf("Merged arguments: %v", mergedArgs)
		} else {
			logger.Warnf("No configured arguments found for server %s", serverOrImage)
			mergedArgs = cmdArgsMap
		}
	} else {
		logger.Debugf("No config found for server %s", serverOrImage)
		mergedArgs = cmdArgsMap
	}

	return mergedArgs
}

// mergeArguments merges arguments from config and command line
// In increasing order of precedence:
// 1. Global arguments
// 2. Server-specific arguments
// 3. Command line arguments
func mergeArguments(globalArgs map[string]string, serverArgs map[string]string, cmdArgs map[string]string) map[string]string {
	// Start with global args
	merged := make(map[string]string)
	for k, v := range globalArgs {
		merged[k] = v
	}

	// Override with server-specific arguments
	for k, v := range serverArgs {
		merged[k] = v
	}

	// Override with command line args
	for k, v := range cmdArgs {
		merged[k] = v
	}

	return merged
}

// argsMapToSlice converts a map of arguments to a slice of strings
func argsMapToSlice(args map[string]string) []string {
	// Convert back to slice of strings
	var result []string
	for k, v := range args {
		if v == "" {
			result = append(result, fmt.Sprintf("--%s", k))
		} else {
			result = append(result, fmt.Sprintf("--%s=%s", k, v))
		}
	}

	return result
}

// ValidateAndNormaliseHostFlag validates and normalizes the host flag resolving it to an IP address if hostname is provided
func ValidateAndNormaliseHostFlag(host string) (string, error) {
	// Check if the host is a valid IP address
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.To4() == nil {
			return "", fmt.Errorf("IPv6 addresses are not supported: %s", host)
		}
		return host, nil
	}

	// If not an IP address, resolve the hostname to an IP address
	addrs, err := net.LookupHost(host)
	if err != nil {
		return "", fmt.Errorf("invalid host: %s", host)
	}

	// Use the first IPv4 address found
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}

	return "", fmt.Errorf("could not resolve host: %s", host)
}

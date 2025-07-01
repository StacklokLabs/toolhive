package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stacklok/toolhive/pkg/certs"
	"github.com/stacklok/toolhive/pkg/client"
	"github.com/stacklok/toolhive/pkg/config"
	"github.com/stacklok/toolhive/pkg/container"
	rt "github.com/stacklok/toolhive/pkg/container/runtime"
	"github.com/stacklok/toolhive/pkg/labels"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/networking"
	"github.com/stacklok/toolhive/pkg/registry"
	"github.com/stacklok/toolhive/pkg/transport"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage application configuration",
	Long:  "The config command provides subcommands to manage application configuration settings.",
}

var listRegisteredClientsCmd = &cobra.Command{
	Use:   "list-registered-clients",
	Short: "List all registered MCP clients",
	Long:  "List all clients that are registered for MCP server configuration.",
	RunE:  listRegisteredClientsCmdFunc,
}

var registerClientCmd = &cobra.Command{
	Use:   "register-client [client]",
	Short: "Register a client for MCP server configuration",
	Long: `Register a client for MCP server configuration.
Valid clients are:
  - claude-code: Claude Code CLI
  - cline: Cline extension for VS Code
  - cursor: Cursor editor
  - roo-code: Roo Code extension for VS Code
  - vscode: Visual Studio Code
  - vscode-insider: Visual Studio Code Insiders edition`,
	Args: cobra.ExactArgs(1),
	RunE: registerClientCmdFunc,
}

var removeClientCmd = &cobra.Command{
	Use:   "remove-client [client]",
	Short: "Remove a client from MCP server configuration",
	Long: `Remove a client from MCP server configuration.
Valid clients are:
  - claude-code: Claude Code CLI
  - cline: Cline extension for VS Code
  - cursor: Cursor editor
  - roo-code: Roo Code extension for VS Code
  - vscode: Visual Studio Code
  - vscode-insider: Visual Studio Code Insiders edition`,
	Args: cobra.ExactArgs(1),
	RunE: removeClientCmdFunc,
}

var setCACertCmd = &cobra.Command{
	Use:   "set-ca-cert <path>",
	Short: "Set the default CA certificate for container builds",
	Long: `Set the default CA certificate file path that will be used for all container builds.
This is useful in corporate environments with TLS inspection where custom CA certificates are required.

Example:
  thv config set-ca-cert /path/to/corporate-ca.crt`,
	Args: cobra.ExactArgs(1),
	RunE: setCACertCmdFunc,
}

var getCACertCmd = &cobra.Command{
	Use:   "get-ca-cert",
	Short: "Get the currently configured CA certificate path",
	Long:  "Display the path to the CA certificate file that is currently configured for container builds.",
	RunE:  getCACertCmdFunc,
}

var unsetCACertCmd = &cobra.Command{
	Use:   "unset-ca-cert",
	Short: "Remove the configured CA certificate",
	Long:  "Remove the CA certificate configuration, reverting to default behavior without custom CA certificates.",
	RunE:  unsetCACertCmdFunc,
}

var setRegistryURLCmd = &cobra.Command{
	Use:   "set-registry-url <url>",
	Short: "Set the MCP server registry URL",
	Long: `Set the URL for the remote MCP server registry.
This allows you to use a custom registry instead of the built-in one.

Example:
  thv config set-registry-url https://example.com/registry.json`,
	Args: cobra.ExactArgs(1),
	RunE: setRegistryURLCmdFunc,
}

var getRegistryURLCmd = &cobra.Command{
	Use:   "get-registry-url",
	Short: "Get the currently configured registry URL",
	Long:  "Display the URL of the remote registry that is currently configured.",
	RunE:  getRegistryURLCmdFunc,
}

var unsetRegistryURLCmd = &cobra.Command{
	Use:   "unset-registry-url",
	Short: "Remove the configured registry URL",
	Long:  "Remove the registry URL configuration, reverting to the built-in registry.",
	RunE:  unsetRegistryURLCmdFunc,
}

var addDefaultServerCmd = &cobra.Command{
	Use:   "add-default-server [server]",
	Short: "Add a server to the list of default servers",
	Long: `Add a server to the list of default servers that will be started when running 'thv run' without arguments.
The server must exist in the registry.

Example:
  thv config add-default-server my-server`,
	Args: cobra.ExactArgs(1),
	RunE: addDefaultServerCmdFunc,
}

var removeDefaultServerCmd = &cobra.Command{
	Use:   "remove-default-server [server]",
	Short: "Remove a server from the list of default servers",
	Long: `Remove a server from the list of default servers.
This only removes the server from the default list, it does not delete or stop the server.

Example:
  thv config remove-default-server my-server`,
	Args: cobra.ExactArgs(1),
	RunE: removeDefaultServerCmdFunc,
}

var (
	allowPrivateRegistryIp bool
)

var (
	resetServerArgsAll bool
)

var resetServerArgsCmd = &cobra.Command{
	Use:   "reset-server-args [SERVER_NAME]",
	Short: "Reset/delete arguments for MCP servers from the config",
	Long: `Reset/delete configured arguments for MCP servers from the config.
This command removes saved arguments from the config file.
Future runs of the affected servers will not use any pre-configured arguments unless explicitly provided.

Examples:
  # Reset arguments for a specific server
  thv config reset-server-args my-server

  # Reset arguments for all servers
  thv config reset-server-args --all`,
	Args: cobra.MaximumNArgs(1),
	RunE: resetServerArgsCmdFunc,
	PreRunE: func(_ *cobra.Command, args []string) error {
		if !resetServerArgsAll && len(args) == 0 {
			return fmt.Errorf("server name is required when --all flag is not set")
		}
		if resetServerArgsAll && len(args) > 0 {
			return fmt.Errorf("server name cannot be specified when using --all flag")
		}
		return nil
	},
}

var setGlobalServerArgsCmd = &cobra.Command{
	Use:   "set-global-server-args KEY=VALUE [KEY=VALUE...]",
	Short: "Set global arguments for all MCP servers",
	Long: `Set global arguments that will be applied to all MCP servers.
These arguments will be used as defaults for all servers unless overridden by server-specific arguments.

Example:
  thv config set-global-server-args debug=true log-level=info`,
	Args: cobra.MinimumNArgs(1),
	RunE: setGlobalServerArgsCmdFunc,
}

var resetGlobalServerArgsCmd = &cobra.Command{
	Use:   "reset-global-server-args",
	Short: "Reset/delete global arguments for all MCP servers",
	Long: `Reset/delete global arguments from the config.
This command removes all saved global arguments from the config file.
Future runs of servers will not use any pre-configured global arguments.`,
	Args: cobra.NoArgs,
	RunE: resetGlobalServerArgsCmdFunc,
}

func init() {
	// Add config command to root command
	rootCmd.AddCommand(configCmd)

	// Add subcommands to config command
	configCmd.AddCommand(registerClientCmd)
	configCmd.AddCommand(removeClientCmd)
	configCmd.AddCommand(listRegisteredClientsCmd)
	configCmd.AddCommand(setCACertCmd)
	configCmd.AddCommand(getCACertCmd)
	configCmd.AddCommand(unsetCACertCmd)
	configCmd.AddCommand(setRegistryURLCmd)
	configCmd.AddCommand(getRegistryURLCmd)
	configCmd.AddCommand(unsetRegistryURLCmd)
	configCmd.AddCommand(resetServerArgsCmd)
	configCmd.AddCommand(setGlobalServerArgsCmd)
	configCmd.AddCommand(resetGlobalServerArgsCmd)
	configCmd.AddCommand(addDefaultServerCmd)
	configCmd.AddCommand(removeDefaultServerCmd)

	resetServerArgsCmd.Flags().BoolVarP(&resetServerArgsAll, "all", "a", false,
		"Reset arguments for all MCP servers")

	// Add OTEL parent command to config
	configCmd.AddCommand(OtelCmd)
}

func registerClientCmdFunc(cmd *cobra.Command, args []string) error {
	clientType := args[0]

	// Validate the client type
	switch clientType {
	case "roo-code", "cline", "cursor", "claude-code", "vscode-insider", "vscode":
		// Valid client type
	default:
		return fmt.Errorf(
			"invalid client type: %s (valid types: roo-code, cline, cursor, claude-code, vscode, vscode-insider)",
			clientType)
	}

	err := config.UpdateConfig(func(c *config.Config) {
		// Check if client is already registered and skip.
		for _, registeredClient := range c.Clients.RegisteredClients {
			if registeredClient == clientType {
				fmt.Printf("Client %s is already registered, skipping...\n", clientType)
				return
			}
		}

		// Add the client to the registered clients list
		c.Clients.RegisteredClients = append(c.Clients.RegisteredClients, clientType)
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Successfully registered client: %s\n", clientType)

	// Add currently running MCPs to the newly registered client
	if err := addRunningMCPsToClient(cmd.Context(), clientType); err != nil {
		fmt.Printf("Warning: Failed to add running MCPs to client: %v\n", err)
	}

	return nil
}

func removeClientCmdFunc(_ *cobra.Command, args []string) error {
	clientType := args[0]

	// Validate the client type
	switch clientType {
	case "roo-code", "cline", "cursor", "claude-code", "vscode-insider", "vscode":
		// Valid client type
	default:
		return fmt.Errorf(
			"invalid client type: %s (valid types: roo-code, cline, cursor, claude-code, vscode, vscode-insider)",
			clientType)
	}

	err := config.UpdateConfig(func(c *config.Config) {
		// Find and remove the client from the registered clients list
		found := false
		for i, registeredClient := range c.Clients.RegisteredClients {
			if registeredClient == clientType {
				// Remove the client by appending the slice before and after the index
				c.Clients.RegisteredClients = append(c.Clients.RegisteredClients[:i], c.Clients.RegisteredClients[i+1:]...)
				found = true
				break
			}
		}
		if found {
			fmt.Printf("Client %s removed from registered clients.\n", clientType)
		} else {
			fmt.Printf("Client %s not found in registered clients.\n", clientType)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Successfully removed client: %s\n", clientType)
	return nil
}

func getFilteredClientConfigs(clientName string) ([]client.ConfigFile, error) {
	clientConfigs, err := client.FindClientConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed to find client configurations: %w", err)
	}
	var filtered []client.ConfigFile
	for _, clientConfig := range clientConfigs {
		if clientConfig.ClientType == client.MCPClient(clientName) {
			filtered = append(filtered, clientConfig)
		}
	}
	return filtered, nil
}

// addRunningMCPsToClient adds currently running MCP servers to the specified client's configuration
func addRunningMCPsToClient(ctx context.Context, clientName string) error {
	// Create container runtime
	runningContainers, err := getRunningToolHiveContainers(ctx)
	if err != nil {
		return err
	}

	if len(runningContainers) == 0 {
		// No running servers, nothing to do
		return nil
	}

	filteredClientConfigs, err := getFilteredClientConfigs(clientName)
	if err != nil {
		return err
	}

	// If no configs found, nothing to do
	if len(filteredClientConfigs) == 0 {
		return nil
	}

	// For each running container, add it to the client configuration
	for _, c := range runningContainers {
		// Get container name from labels
		name := labels.GetContainerName(c.Labels)
		if name == "" {
			name = c.Name // Fallback to container name
		}

		// Get tool type from labels
		toolType := labels.GetToolType(c.Labels)

		// Only include containers with tool type "mcp"
		if toolType != "mcp" {
			continue
		}

		// Get port from labels
		port, err := labels.GetPort(c.Labels)
		if err != nil {
			continue // Skip if we can't get the port
		}

		transportType := labels.GetTransportType(c.Labels)

		// Generate URL for the MCP server
		url := client.GenerateMCPServerURL(transportType, transport.LocalhostIPv4, port, name)

		// Update each configuration file
		for _, clientConfig := range filteredClientConfigs {
			// Update the MCP server configuration with locking
			if err := client.Upsert(clientConfig, name, url, transportType); err != nil {
				logger.Warnf("Warning: Failed to update MCP server configuration in %s: %v", clientConfig.Path, err)
				continue
			}

			fmt.Printf("Added MCP server %s to client %s\n", name, clientName)
		}
	}

	return nil
}

func getRunningToolHiveContainers(ctx context.Context) ([]rt.ContainerInfo, error) {
	runtime, err := container.NewFactory().Create(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create container runtime: %v", err)
	}

	// List workloads
	containers, err := runtime.ListWorkloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %v", err)
	}

	// Filter containers to only show those managed by ToolHive and running
	var runningContainers []rt.ContainerInfo
	for _, c := range containers {
		if labels.IsToolHiveContainer(c.Labels) && c.State == "running" {
			runningContainers = append(runningContainers, c)
		}
	}
	return runningContainers, nil
}

func setCACertCmdFunc(_ *cobra.Command, args []string) error {
	certPath := filepath.Clean(args[0])

	// Validate that the file exists and is readable
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("CA certificate file not found or not accessible: %w", err)
	}

	// Read and validate the certificate
	certContent, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate file: %w", err)
	}

	// Validate the certificate format
	if err := certs.ValidateCACertificate(certContent); err != nil {
		return fmt.Errorf("invalid CA certificate: %w", err)
	}

	// Update the configuration
	err = config.UpdateConfig(func(c *config.Config) {
		c.CACertificatePath = certPath
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Successfully set CA certificate path: %s\n", certPath)
	return nil
}

func getCACertCmdFunc(_ *cobra.Command, _ []string) error {
	cfg := config.GetConfig()

	if cfg.CACertificatePath == "" {
		fmt.Println("No CA certificate is currently configured.")
		return nil
	}

	fmt.Printf("Current CA certificate path: %s\n", cfg.CACertificatePath)

	// Check if the file still exists
	if _, err := os.Stat(cfg.CACertificatePath); err != nil {
		fmt.Printf("Warning: The configured CA certificate file is not accessible: %v\n", err)
	}

	return nil
}

func unsetCACertCmdFunc(_ *cobra.Command, _ []string) error {
	cfg := config.GetConfig()

	if cfg.CACertificatePath == "" {
		fmt.Println("No CA certificate is currently configured.")
		return nil
	}

	// Update the configuration
	err := config.UpdateConfig(func(c *config.Config) {
		c.CACertificatePath = ""
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Println("Successfully removed CA certificate configuration.")
	return nil
}

func setRegistryURLCmdFunc(_ *cobra.Command, args []string) error {
	registryURL := args[0]

	// Basic URL validation - check if it starts with http:// or https://
	if registryURL != "" && !strings.HasPrefix(registryURL, "http://") && !strings.HasPrefix(registryURL, "https://") {
		return fmt.Errorf("registry URL must start with http:// or https://")
	}

	if !allowPrivateRegistryIp {
		registryClient := networking.GetHttpClient(false)
		_, err := registryClient.Get(registryURL)
		if err != nil && strings.Contains(fmt.Sprint(err), networking.ErrPrivateIpAddress) {
			return err
		}
	}

	// Update the configuration
	err := config.UpdateConfig(func(c *config.Config) {
		c.RegistryUrl = registryURL
		c.AllowPrivateRegistryIp = allowPrivateRegistryIp
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Successfully set registry URL: %s\n", registryURL)
	if allowPrivateRegistryIp {
		fmt.Print("Successfully enabled use of private IP addresses for the remote registry\n")
		fmt.Print("Caution: allowing registry URLs containing private IP addresses may decrease your security.\n" +
			"Make sure you trust any remote registries you configure with ToolHive.")
	} else {
		fmt.Printf("Use of private IP addresses for the remote registry has been disabled" +
			" as it's not needed for the provided registry.\n")
	}

	return nil
}

func getRegistryURLCmdFunc(_ *cobra.Command, _ []string) error {
	cfg := config.GetConfig()

	if cfg.RegistryUrl == "" {
		fmt.Println("No custom registry URL is currently configured. Using built-in registry.")
		return nil
	}

	fmt.Printf("Current registry URL: %s\n", cfg.RegistryUrl)
	return nil
}

func unsetRegistryURLCmdFunc(_ *cobra.Command, _ []string) error {
	cfg := config.GetConfig()

	if cfg.RegistryUrl == "" {
		fmt.Println("No custom registry URL is currently configured.")
		return nil
	}

	// Update the configuration
	err := config.UpdateConfig(func(c *config.Config) {
		c.RegistryUrl = ""
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Println("Successfully removed registry URL configuration. Will use built-in registry.")
	return nil
}

func listRegisteredClientsCmdFunc(_ *cobra.Command, _ []string) error {
	// Get the current config
	cfg := config.GetConfig()

	// Check if there are any registered clients
	if len(cfg.Clients.RegisteredClients) == 0 {
		fmt.Println("No clients are currently registered.")
		return nil
	}

	// Print the list of registered clients
	fmt.Println("Registered clients:")
	for _, clientName := range cfg.Clients.RegisteredClients {
		fmt.Printf("  - %s\n", clientName)
	}

	return nil
}

func resetServerArgsCmdFunc(_ *cobra.Command, args []string) error {
	// Load the config
	cfg, err := config.LoadOrCreateConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	if resetServerArgsAll {
		// Delete all server arguments
		if err := cfg.DeleteAllServerArgs(); err != nil {
			return fmt.Errorf("failed to reset all server arguments: %v", err)
		}
		logger.Info("Successfully reset arguments for all servers")
	} else {
		// Delete arguments for specific server
		serverName := args[0]
		if err := cfg.DeleteServerArgs(serverName); err != nil {
			return fmt.Errorf("failed to reset server arguments: %v", err)
		}
		logger.Infof("Successfully reset arguments for server %s", serverName)
	}

	return nil
}

func setGlobalServerArgsCmdFunc(_ *cobra.Command, args []string) error {
	// Parse the arguments into a map
	argsMap := make(map[string]string)
	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid argument format: %s (expected KEY=VALUE)", arg)
		}
		argsMap[parts[0]] = parts[1]
	}

	// Load the config
	cfg, err := config.LoadOrCreateConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Set the global arguments
	if err := cfg.SetGlobalServerArgs(argsMap); err != nil {
		return fmt.Errorf("failed to set global server arguments: %v", err)
	}

	logger.Info("Successfully set global server arguments")
	return nil
}

func resetGlobalServerArgsCmdFunc(_ *cobra.Command, _ []string) error {
	// Load the config
	cfg, err := config.LoadOrCreateConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Reset the global arguments
	if err := cfg.DeleteGlobalServerArgs(); err != nil {
		return fmt.Errorf("failed to reset global server arguments: %v", err)
	}

	logger.Info("Successfully reset global server arguments")
	return nil
}

func addDefaultServerCmdFunc(_ *cobra.Command, args []string) error {
	serverName := args[0]

	// Load the registry to validate the server exists
	provider, err := registry.GetDefaultProvider()
	if err != nil {
		return fmt.Errorf("failed to get registry provider: %v", err)
	}

	// Check if the server exists in the registry
	if _, err := provider.GetServer(serverName); err != nil {
		return fmt.Errorf("server %q not found in registry: %w", serverName, err)
	}

	// Update the configuration
	err = config.UpdateConfig(func(c *config.Config) {
		// Check if server is already in default servers
		for _, s := range c.DefaultServers {
			if s == serverName {
				fmt.Printf("Server %q is already in default servers list\n", serverName)
				return
			}
		}

		// Add the server to default servers
		c.DefaultServers = append(c.DefaultServers, serverName)
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Successfully added %q to default servers\n", serverName)
	return nil
}

func removeDefaultServerCmdFunc(_ *cobra.Command, args []string) error {
	serverName := args[0]

	// Update the configuration
	err := config.UpdateConfig(func(c *config.Config) {
		// Find and remove the server from the default servers list
		found := false
		for i, s := range c.DefaultServers {
			if s == serverName {
				// Remove the server by appending the slice before and after the index
				c.DefaultServers = append(c.DefaultServers[:i], c.DefaultServers[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("Server %q not found in default servers list\n", serverName)
			return
		}
	})
	if err != nil {
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	fmt.Printf("Successfully removed %q from default servers\n", serverName)
	return nil
}

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
  - jetbrains-copilot: Copilot extension for JetBrains IDEs
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
  - jetbrains-copilot: Copilot extension for JetBrains IDEs
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

var (
	allowPrivateRegistryIp bool
)

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
	setRegistryURLCmd.Flags().BoolVarP(
		&allowPrivateRegistryIp,
		"allow-private-ip",
		"p",
		false,
		"Allow setting the registry URL, even if it references a private IP address",
	)
	configCmd.AddCommand(getRegistryURLCmd)
	configCmd.AddCommand(unsetRegistryURLCmd)

	// Add OTEL parent command to config
	configCmd.AddCommand(OtelCmd)
}

func registerClientCmdFunc(cmd *cobra.Command, args []string) error {
	clientType := args[0]

	// Validate the client type
	switch clientType {
	case "roo-code", "cline", "cursor", "claude-code", "jetbrains-copilot", "vscode-insider", "vscode":
		// Valid client type
	default:
		return fmt.Errorf(
			"invalid client type: %s (valid types: roo-code, cline, cursor, claude-code, jetbrains-copilot, vscode, vscode-insider)",
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
	case "roo-code", "cline", "cursor", "claude-code", "jetbrains-copilot", "vscode-insider", "vscode":
		// Valid client type
	default:
		return fmt.Errorf(
			"invalid client type: %s (valid types: roo-code, cline, cursor, claude-code, jetbrains-copilot, vscode, vscode-insider)",
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

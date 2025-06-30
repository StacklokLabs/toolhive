package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/versions"
)

var (
	mcpServerURL string
	mcpFormat    string
	mcpTimeout   time.Duration
)

func newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Interact with MCP servers for debugging",
		Long:  `The mcp command provides subcommands to interact with MCP (Model Context Protocol) servers for debugging purposes.`,
	}

	// Create list command
	listCmd := &cobra.Command{
		Use:   "list [tools|resources|prompts]",
		Short: "List MCP server capabilities",
		Long:  `List tools, resources, and prompts available from an MCP server. Use subcommands to list specific types.`,
		RunE:  mcpListCmdFunc,
	}

	// Create specific list subcommands
	toolsCmd := &cobra.Command{
		Use:   "tools",
		Short: "List available tools from MCP server",
		Long:  `List all tools available from the specified MCP server.`,
		RunE:  mcpListToolsCmdFunc,
	}

	resourcesCmd := &cobra.Command{
		Use:   "resources",
		Short: "List available resources from MCP server",
		Long:  `List all resources available from the specified MCP server.`,
		RunE:  mcpListResourcesCmdFunc,
	}

	promptsCmd := &cobra.Command{
		Use:   "prompts",
		Short: "List available prompts from MCP server",
		Long:  `List all prompts available from the specified MCP server.`,
		RunE:  mcpListPromptsCmdFunc,
	}

	// Add flags to all MCP commands
	addMCPFlags(listCmd)
	addMCPFlags(toolsCmd)
	addMCPFlags(resourcesCmd)
	addMCPFlags(promptsCmd)

	// Add specific list subcommands to list command
	listCmd.AddCommand(toolsCmd)
	listCmd.AddCommand(resourcesCmd)
	listCmd.AddCommand(promptsCmd)

	// Add list subcommand to mcp
	cmd.AddCommand(listCmd)

	return cmd
}

func addMCPFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&mcpServerURL, "server", "", "MCP server URL (required)")
	cmd.Flags().StringVar(&mcpFormat, "format", FormatText, "Output format (json or text)")
	cmd.Flags().DurationVar(&mcpTimeout, "timeout", 30*time.Second, "Connection timeout")
	_ = cmd.MarkFlagRequired("server")
}

// mcpListCmdFunc lists all capabilities (tools, resources, prompts)
func mcpListCmdFunc(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), mcpTimeout)
	defer cancel()

	mcpClient, err := createMCPClient()
	if err != nil {
		return err
	}
	defer mcpClient.Close()

	if err := initializeMCPClient(ctx, mcpClient); err != nil {
		return err
	}

	// Collect all data
	data := make(map[string]interface{})

	// List tools
	if tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		logger.Warnf("Failed to list tools: %v", err)
		data["tools"] = []mcp.Tool{}
	} else {
		data["tools"] = tools.Tools
	}

	// List resources
	if resources, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{}); err != nil {
		logger.Warnf("Failed to list resources: %v", err)
		data["resources"] = []mcp.Resource{}
	} else {
		data["resources"] = resources.Resources
	}

	// List prompts
	if prompts, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{}); err != nil {
		logger.Warnf("Failed to list prompts: %v", err)
		data["prompts"] = []mcp.Prompt{}
	} else {
		data["prompts"] = prompts.Prompts
	}

	return outputMCPData(data, mcpFormat)
}

// mcpListToolsCmdFunc lists only tools
func mcpListToolsCmdFunc(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), mcpTimeout)
	defer cancel()

	mcpClient, err := createMCPClient()
	if err != nil {
		return err
	}
	defer mcpClient.Close()

	if err := initializeMCPClient(ctx, mcpClient); err != nil {
		return err
	}

	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	return outputMCPData(map[string]interface{}{"tools": result.Tools}, mcpFormat)
}

// mcpListResourcesCmdFunc lists only resources
func mcpListResourcesCmdFunc(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), mcpTimeout)
	defer cancel()

	mcpClient, err := createMCPClient()
	if err != nil {
		return err
	}
	defer mcpClient.Close()

	if err := initializeMCPClient(ctx, mcpClient); err != nil {
		return err
	}

	result, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	return outputMCPData(map[string]interface{}{"resources": result.Resources}, mcpFormat)
}

// mcpListPromptsCmdFunc lists only prompts
func mcpListPromptsCmdFunc(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), mcpTimeout)
	defer cancel()

	mcpClient, err := createMCPClient()
	if err != nil {
		return err
	}
	defer mcpClient.Close()

	if err := initializeMCPClient(ctx, mcpClient); err != nil {
		return err
	}

	result, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return fmt.Errorf("failed to list prompts: %w", err)
	}

	return outputMCPData(map[string]interface{}{"prompts": result.Prompts}, mcpFormat)
}

// createMCPClient creates an MCP client based on the server URL
func createMCPClient() (*client.Client, error) {
	// For now, we'll use SSE client as the default
	// In the future, we could auto-detect or allow specifying the transport type
	mcpClient, err := client.NewSSEMCPClient(mcpServerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}
	return mcpClient, nil
}

// initializeMCPClient initializes the MCP client connection
func initializeMCPClient(ctx context.Context, mcpClient *client.Client) error {
	// Start the transport
	if err := mcpClient.Start(ctx); err != nil {
		return fmt.Errorf("failed to start MCP transport: %w", err)
	}

	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.Capabilities = mcp.ClientCapabilities{
		// Basic client capabilities for listing
	}
	versionInfo := versions.GetVersionInfo()
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "toolhive-cli",
		Version: versionInfo.Version,
	}

	_, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	return nil
}

// outputMCPData outputs the MCP data in the specified format
func outputMCPData(data map[string]interface{}, format string) error {
	switch format {
	case FormatJSON:
		return outputMCPJSON(data)
	default:
		return outputMCPText(data)
	}
}

// outputMCPJSON outputs MCP data in JSON format
func outputMCPJSON(data map[string]interface{}) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(jsonData))
	return nil
}

// outputMCPText outputs MCP data in text format
func outputMCPText(data map[string]interface{}) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	hasData := outputMCPTools(w, data) ||
		outputMCPResources(w, data) ||
		outputMCPPrompts(w, data)

	if !hasData {
		fmt.Println("No tools, resources, or prompts found")
		return nil
	}

	return w.Flush()
}

// outputMCPTools outputs tools data to the tabwriter
func outputMCPTools(w *tabwriter.Writer, data map[string]interface{}) bool {
	tools, ok := data["tools"].([]mcp.Tool)
	if !ok || len(tools) == 0 {
		return false
	}

	fmt.Fprintln(w, "TOOLS:")
	fmt.Fprintln(w, "NAME\tDESCRIPTION")
	for _, tool := range tools {
		fmt.Fprintf(w, "%s\t%s\n", tool.Name, tool.Description)
	}
	fmt.Fprintln(w, "")
	return true
}

// outputMCPResources outputs resources data to the tabwriter
func outputMCPResources(w *tabwriter.Writer, data map[string]interface{}) bool {
	resources, ok := data["resources"].([]mcp.Resource)
	if !ok || len(resources) == 0 {
		return false
	}

	fmt.Fprintln(w, "RESOURCES:")
	fmt.Fprintln(w, "NAME\tURI\tDESCRIPTION\tMIME_TYPE")
	for _, resource := range resources {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			resource.Name, resource.URI, resource.Description, resource.MIMEType)
	}
	fmt.Fprintln(w, "")
	return true
}

// outputMCPPrompts outputs prompts data to the tabwriter
func outputMCPPrompts(w *tabwriter.Writer, data map[string]interface{}) bool {
	prompts, ok := data["prompts"].([]mcp.Prompt)
	if !ok || len(prompts) == 0 {
		return false
	}

	fmt.Fprintln(w, "PROMPTS:")
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tARGUMENTS")
	for _, prompt := range prompts {
		argStr := formatPromptArguments(prompt.Arguments)
		fmt.Fprintf(w, "%s\t%s\t%s\n", prompt.Name, prompt.Description, argStr)
	}
	fmt.Fprintln(w, "")
	return true
}

// formatPromptArguments formats the prompt arguments for display
func formatPromptArguments(arguments []mcp.PromptArgument) string {
	argCount := len(arguments)
	if argCount == 0 {
		return "0"
	}

	argNames := make([]string, len(arguments))
	for i, arg := range arguments {
		argNames[i] = arg.Name
	}
	return fmt.Sprintf("%d (%v)", argCount, argNames)
}

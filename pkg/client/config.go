// Package client provides utilities for managing client configurations
// and interacting with MCP servers.
package client

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/tailscale/hujson"

	"github.com/stacklok/toolhive/pkg/config"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/transport/ssecommon"
)

// lockTimeout is the maximum time to wait for a file lock
const lockTimeout = 1 * time.Second

// MCPClient is an enum of supported MCP clients.
type MCPClient string

const (
	// RooCode represents the Roo Code extension for VS Code.
	RooCode MCPClient = "roo-code"
	// Cline represents the Cline extension for VS Code.
	Cline MCPClient = "cline"
	// Cursor represents the Cursor editor.
	Cursor MCPClient = "cursor"
	// VSCodeInsider represents the VS Code Insiders editor.
	VSCodeInsider MCPClient = "vscode-insider"
	// VSCode represents the standard VS Code editor.
	VSCode MCPClient = "vscode"
	// ClaudeCode represents the Claude Code CLI.
	ClaudeCode MCPClient = "claude-code"
)

// Extension is extension of the client config file.
type Extension string

const (
	// JSON represents a JSON extension.
	JSON Extension = "json"
)

// mcpClientConfig represents a configuration path for a supported MCP client.
type mcpClientConfig struct {
	ClientType           MCPClient
	Description          string
	RelPath              []string
	PlatformPrefix       map[string][]string
	MCPServersPathPrefix string
	Extension            Extension
}

var supportedClientIntegrations = []mcpClientConfig{
	{
		ClientType:  RooCode,
		Description: "VS Code Roo Code extension",
		RelPath: []string{
			"Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "mcp_settings.json",
		},
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		MCPServersPathPrefix: "/mcpServers",
		Extension:            JSON,
	},
	{
		ClientType:  Cline,
		Description: "VS Code Cline extension",
		RelPath: []string{
			"Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json",
		},
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		MCPServersPathPrefix: "/mcpServers",
		Extension:            JSON,
	},
	{
		ClientType:  VSCodeInsider,
		Description: "Visual Studio Code Insiders",
		RelPath: []string{
			"Code - Insiders", "User", "settings.json",
		},
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		MCPServersPathPrefix: "/mcp/servers",
		Extension:            JSON,
	},
	{
		ClientType:  VSCode,
		Description: "Visual Studio Code",
		RelPath: []string{
			"Code", "User", "settings.json",
		},
		MCPServersPathPrefix: "/mcp/servers",
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		Extension: JSON,
	},
	{
		ClientType:           Cursor,
		Description:          "Cursor editor",
		MCPServersPathPrefix: "/mcpServers",
		RelPath:              []string{".cursor", "mcp.json"},
		Extension:            JSON,
	},
	{
		ClientType:           ClaudeCode,
		Description:          "Claude Code CLI",
		MCPServersPathPrefix: "/mcpServers",
		RelPath:              []string{".claude.json"},
		Extension:            JSON,
	},
}

// ConfigFile represents a client configuration file
type ConfigFile struct {
	Path          string
	ClientType    MCPClient
	ConfigUpdater ConfigUpdater
	Extension     Extension
}

// MCPServerConfig represents an MCP server configuration in a client config file
type MCPServerConfig struct {
	URL string `json:"url,omitempty"`
}

// FindClientConfigs searches for client configuration files in standard locations
func FindClientConfigs() ([]ConfigFile, error) {
	// Start by assuming all clients are enabled
	var filters []MCPClient
	appConfig := config.GetConfig()
	// If we are not using auto-discovery, we need to filter the set of clients to configure.
	if !appConfig.Clients.AutoDiscovery {
		if len(appConfig.Clients.RegisteredClients) > 0 {
			filters = make([]MCPClient, len(appConfig.Clients.RegisteredClients))
			for _, client := range appConfig.Clients.RegisteredClients {
				// Not validating client names here - assuming that A) they are
				// validated when set, and B) they will be dropped later if not valid.
				filters = append(filters, MCPClient(client))
			}
		} else {
			// No clients configured - exit early.
			return nil, nil
		}
	}

	// retrieve the metadata of the config files
	configFiles, err := retrieveConfigFilesMetadata(filters)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve client config metadata: %w", err)
	}

	// validate the format of the config files
	err = validateConfigFilesFormat(configFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to validate config file format: %w", err)
	}
	return configFiles, nil
}

// Upsert updates/inserts an MCP server in a client configuration file
// It is a wrapper around the ConfigUpdater.Upsert method. Because the
// ConfigUpdater is different for each client type, we need to handle
// the different types of McpServer objects. For example, VSCode and ClaudeCode allows
// for a `type` field, but Cursor and others do not. This allows us to
// build up more complex MCP server configurations for different clients
// without leaking them into the CMD layer.
func Upsert(cf ConfigFile, name string, url string) error {
	if cf.ClientType == VSCode || cf.ClientType == VSCodeInsider || cf.ClientType == ClaudeCode {
		return cf.ConfigUpdater.Upsert(name, MCPServer{Url: url, Type: "sse"})
	}

	return cf.ConfigUpdater.Upsert(name, MCPServer{Url: url})
}

// GenerateMCPServerURL generates the URL for an MCP server
func GenerateMCPServerURL(host string, port int, containerName string) string {
	// The URL format is: http://host:port/sse#container-name
	// Both SSE and STDIO transport types use an SSE proxy
	return fmt.Sprintf("http://%s:%d%s#%s", host, port, ssecommon.HTTPSSEEndpoint, containerName)
}

// retrieveConfigFilesMetadata retrieves the metadata for client configuration files.
// It returns a list of ConfigFile objects, which contain metadata about the file that
// can be used when performing operations on the file.
func retrieveConfigFilesMetadata(filters []MCPClient) ([]ConfigFile, error) {
	var configFiles []ConfigFile

	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	for _, cfg := range supportedClientIntegrations {
		// If filters are specified, filter out the clients we don't care about.
		if len(filters) > 0 && !slices.Contains(filters, cfg.ClientType) {
			continue
		}

		path := buildConfigFilePath(cfg.RelPath, cfg.PlatformPrefix, []string{home})

		err := validateConfigFileExists(path)
		if err != nil {
			logger.Warnf("failed to validate config file: %w", err)
			continue
		}

		configUpdater := &JSONConfigUpdater{Path: path, MCPServersPathPrefix: cfg.MCPServersPathPrefix}

		clientConfig := ConfigFile{
			Path:          path,
			ConfigUpdater: configUpdater,
			ClientType:    cfg.ClientType,
			Extension:     cfg.Extension,
		}

		configFiles = append(configFiles, clientConfig)
	}

	return configFiles, nil
}

func buildConfigFilePath(relPath []string, platformPrefix map[string][]string, path []string) string {
	if prefix, ok := platformPrefix[runtime.GOOS]; ok {
		path = append(path, prefix...)
	}
	path = append(path, relPath...)
	return filepath.Clean(filepath.Join(path...))
}

// validateConfigFileExists validates that a client configuration file exists.
func validateConfigFileExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", path)
	}
	return nil
}

// validateConfigFileFormat validates the format of a client configuration file
// It returns an error if the file is not valid JSON.
func validateConfigFilesFormat(configFiles []ConfigFile) error {
	for _, cf := range configFiles {
		data, err := os.ReadFile(cf.Path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", cf.Path, err)
		}

		// Default to JSON
		// we don't care about the contents of the file, we just want to validate that it's valid JSON
		_, err = hujson.Parse(data)
		if err != nil {
			return fmt.Errorf("failed to parse JSON for file %s: %w", cf.Path, err)
		}
	}

	return nil
}

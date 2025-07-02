// Package client provides utilities for managing client configurations
// and interacting with MCP servers.
package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tailscale/hujson"

	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/transport/ssecommon"
	"github.com/stacklok/toolhive/pkg/transport/streamable"
	"github.com/stacklok/toolhive/pkg/transport/types"
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
	// JetBrainsCopilot represents the Copilot plugin for JetBrains IDEs.
	JetBrainsCopilot MCPClient = "jetbrains-copilot"
)

// Extension is extension of the client config file.
type Extension string

const (
	// JSON represents a JSON extension.
	JSON Extension = "json"
)

// mcpClientConfig represents a configuration path for a supported MCP client.
type mcpClientConfig struct {
	ClientType                    MCPClient
	Description                   string
	RelPath                       []string
	SettingsFile                  string
	PlatformPrefix                map[string][]string
	MCPServersPathPrefix          string
	Extension                     Extension
	SupportedTransportTypesMap    map[types.TransportType]string // stdio should be mapped to sse
	IsTransportTypeFieldSupported bool
}

var (
	// ErrConfigFileNotFound is returned when a client configuration file is not found
	ErrConfigFileNotFound = fmt.Errorf("client config file not found")
)

var supportedClientIntegrations = []mcpClientConfig{
	{
		ClientType:   RooCode,
		Description:  "VS Code Roo Code extension",
		SettingsFile: "mcp_settings.json",
		RelPath: []string{
			"Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings",
		},
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		MCPServersPathPrefix: "/mcpServers",
		Extension:            JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeStdio:          "sse",
			types.TransportTypeSSE:            "sse",
			types.TransportTypeStreamableHTTP: "streamable-http",
		},
		IsTransportTypeFieldSupported: true,
	},
	{
		ClientType:   Cline,
		Description:  "VS Code Cline extension",
		SettingsFile: "cline_mcp_settings.json",
		RelPath: []string{
			"Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings",
		},
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		MCPServersPathPrefix: "/mcpServers",
		Extension:            JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeSSE:   "sse",
			types.TransportTypeStdio: "sse",
		},
		IsTransportTypeFieldSupported: false,
	},
	{
		ClientType:   VSCodeInsider,
		Description:  "Visual Studio Code Insiders",
		SettingsFile: "settings.json",
		RelPath: []string{
			"Code - Insiders", "User",
		},
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		MCPServersPathPrefix: "/mcp/servers",
		Extension:            JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeStdio:          "sse",
			types.TransportTypeSSE:            "sse",
			types.TransportTypeStreamableHTTP: "http",
		},
		IsTransportTypeFieldSupported: true,
	},
	{
		ClientType:   VSCode,
		Description:  "Visual Studio Code",
		SettingsFile: "settings.json",
		RelPath: []string{
			"Code", "User",
		},
		MCPServersPathPrefix: "/mcp/servers",
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {"Library", "Application Support"},
			"windows": {"AppData", "Roaming"},
		},
		Extension: JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeStdio:          "sse",
			types.TransportTypeSSE:            "sse",
			types.TransportTypeStreamableHTTP: "http",
		},
		IsTransportTypeFieldSupported: true,
	},
	{
		ClientType:           Cursor,
		Description:          "Cursor editor",
		SettingsFile:         "mcp.json",
		MCPServersPathPrefix: "/mcpServers",
		RelPath:              []string{".cursor"},
		Extension:            JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeStdio:          "sse",
			types.TransportTypeSSE:            "sse",
			types.TransportTypeStreamableHTTP: "http",
		},
		// Adding type field is not explicitly required though, Cursor auto-detects and is able to
		// connect to both sse and streamable-http types
		IsTransportTypeFieldSupported: true,
	},
	{
		ClientType:           ClaudeCode,
		Description:          "Claude Code CLI",
		SettingsFile:         ".claude.json",
		MCPServersPathPrefix: "/mcpServers",
		RelPath:              []string{},
		Extension:            JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeStdio:          "sse",
			types.TransportTypeSSE:            "sse",
			types.TransportTypeStreamableHTTP: "http",
		},
		IsTransportTypeFieldSupported: true,
	},
	{
		ClientType:   JetBrainsCopilot,
		Description:  "JetBrains Copilot plugin",
		SettingsFile: "mcp.json",
		RelPath: []string{
			"github-copilot", "intellij",
		},
		MCPServersPathPrefix: "/servers",
		PlatformPrefix: map[string][]string{
			"linux":   {".config"},
			"darwin":  {".config"},
			"windows": {"AppData", "Local"},
		},
		Extension: JSON,
		SupportedTransportTypesMap: map[types.TransportType]string{
			types.TransportTypeStdio:          "sse",
			types.TransportTypeSSE:            "sse",
			types.TransportTypeStreamableHTTP: "http",
		},
		IsTransportTypeFieldSupported: true,
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

// FindClientConfig returns the client configuration file for a given client type.
func FindClientConfig(clientType MCPClient) (*ConfigFile, error) {
	// retrieve the metadata of the config files
	configFile, err := retrieveConfigFileMetadata(clientType)
	if err != nil {
		if errors.Is(err, ErrConfigFileNotFound) {
			// Propagate the error if the file is not found
			return nil, fmt.Errorf("%w: for client %s", ErrConfigFileNotFound, clientType)
		}
		return nil, err
	}

	// validate the format of the config files
	err = validateConfigFileFormat(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to validate config file format: %w", err)
	}
	return configFile, nil
}

// FindClientConfigs searches for client configuration files in standard locations
func FindClientConfigs() ([]ConfigFile, error) {
	clientStatuses, err := GetClientStatus()
	if err != nil {
		return nil, fmt.Errorf("failed to get client status: %w", err)
	}

	var configFiles []ConfigFile
	for _, clientStatus := range clientStatuses {
		if !clientStatus.Installed {
			continue
		}
		cf, err := FindClientConfig(clientStatus.ClientType)
		if err != nil {
			return nil, fmt.Errorf("failed to find client config for %s: %w", clientStatus.ClientType, err)
		}
		configFiles = append(configFiles, *cf)
	}

	return configFiles, nil
}

// CreateClientConfig creates a new client configuration file for a given client type.
func CreateClientConfig(clientType MCPClient) (*ConfigFile, error) {
	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Find the configuration for the requested client type
	var clientCfg *mcpClientConfig
	for _, cfg := range supportedClientIntegrations {
		if cfg.ClientType == clientType {
			clientCfg = &cfg
			break
		}
	}

	if clientCfg == nil {
		return nil, fmt.Errorf("unsupported client type: %s", clientType)
	}

	// Build the path to the configuration file
	path := buildConfigFilePath(clientCfg.SettingsFile, clientCfg.RelPath, clientCfg.PlatformPrefix, []string{home})

	// Validate that the file does not already exist
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil, fmt.Errorf("client config file already exists at %s", path)
	}

	// Create the file if it does not exist
	logger.Infof("Creating new client config file at %s", path)
	err = os.WriteFile(path, []byte("{}"), 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create client config file: %w", err)
	}

	return FindClientConfig(clientType)
}

// Upsert updates/inserts an MCP server in a client configuration file
// It is a wrapper around the ConfigUpdater.Upsert method. Because the
// ConfigUpdater is different for each client type, we need to handle
// the different types of McpServer objects. For example, VSCode and ClaudeCode allows
// for a `type` field, but Cursor and others do not. This allows us to
// build up more complex MCP server configurations for different clients
// without leaking them into the CMD layer.
func Upsert(cf ConfigFile, name string, url string, transportType string) error {
	for i := range supportedClientIntegrations {
		if cf.ClientType != supportedClientIntegrations[i].ClientType {
			continue
		}
		mappedTransportType, ok := supportedClientIntegrations[i].SupportedTransportTypesMap[types.TransportType(transportType)]
		if supportedClientIntegrations[i].IsTransportTypeFieldSupported && ok {
			return cf.ConfigUpdater.Upsert(name, MCPServer{Url: url, Type: mappedTransportType})
		}
		return cf.ConfigUpdater.Upsert(name, MCPServer{Url: url})
	}
	return nil
}

// GenerateMCPServerURL generates the URL for an MCP server
func GenerateMCPServerURL(transportType string, host string, port int, containerName string) string {
	// The URL format is: http://host:port/sse#container-name
	// Both SSE and STDIO transport types use an SSE proxy
	if transportType == types.TransportTypeSSE.String() || transportType == types.TransportTypeStdio.String() {
		return fmt.Sprintf("http://%s:%d%s#%s", host, port, ssecommon.HTTPSSEEndpoint, containerName)
	} else if transportType == types.TransportTypeStreamableHTTP.String() {
		return fmt.Sprintf("http://%s:%d/%s", host, port, streamable.HTTPStreamableHTTPEndpoint)
	}
	return ""
}

// retrieveConfigFileMetadata retrieves the metadata for client configuration files for a given client type.
func retrieveConfigFileMetadata(clientType MCPClient) (*ConfigFile, error) {
	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Find the configuration for the requested client type
	var clientCfg *mcpClientConfig
	for _, cfg := range supportedClientIntegrations {
		if cfg.ClientType == clientType {
			clientCfg = &cfg
			break
		}
	}

	if clientCfg == nil {
		return nil, fmt.Errorf("unsupported client type: %s", clientType)
	}

	// Build the path to the configuration file
	path := buildConfigFilePath(clientCfg.SettingsFile, clientCfg.RelPath, clientCfg.PlatformPrefix, []string{home})

	// Validate that the file exists
	if err := validateConfigFileExists(path); err != nil {
		return nil, err
	}

	// Create a config updater for this file
	configUpdater := &JSONConfigUpdater{
		Path:                 path,
		MCPServersPathPrefix: clientCfg.MCPServersPathPrefix,
	}

	// Return the configuration file metadata
	return &ConfigFile{
		Path:          path,
		ConfigUpdater: configUpdater,
		ClientType:    clientCfg.ClientType,
		Extension:     clientCfg.Extension,
	}, nil
}

func buildConfigFilePath(settingsFile string, relPath []string, platformPrefix map[string][]string, path []string) string {
	if prefix, ok := platformPrefix[runtime.GOOS]; ok {
		path = append(path, prefix...)
	}
	path = append(path, relPath...)
	path = append(path, settingsFile)
	return filepath.Clean(filepath.Join(path...))
}

// validateConfigFileExists validates that a client configuration file exists.
func validateConfigFileExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ErrConfigFileNotFound
	}
	return nil
}

func validateConfigFileFormat(cf *ConfigFile) error {
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
	return nil
}

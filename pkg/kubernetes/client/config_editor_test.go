package client

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"gotest.tools/assert"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

func TestUpsertMCPServerConfig(t *testing.T) {
	t.Parallel()

	logger.Initialize()

	tests := []struct {
		mcpServerPatchPath string // the path used by the patch operation
		mcpServerKeyPath   string // the path used to retrieve the value from the config file (for testing purposes)
		mcpServerName      string // the name of the MCP server to remove
	}{
		{mcpServerPatchPath: "/mcp/servers", mcpServerKeyPath: "mcp.servers", mcpServerName: "testMcpServerUpdate"},
		{mcpServerPatchPath: "/mcpServers", mcpServerKeyPath: "mcpServers", mcpServerName: "testMcpServerUpdate"},
	}

	for _, tt := range tests {

		t.Run("AddNewMCPServer", func(t *testing.T) {
			t.Parallel()

			uniqueId := uuid.New().String()
			tempDir, configPath := setupEmptyTestConfig(t, uniqueId)

			jsu := JSONConfigUpdater{
				Path:                 configPath,
				MCPServersPathPrefix: tt.mcpServerPatchPath,
			}

			mcpServer := MCPServer{
				Url: fmt.Sprintf("test-url-%s", uniqueId),
			}

			err := jsu.Upsert(tt.mcpServerName, mcpServer)
			if err != nil {
				t.Fatalf("Failed to update config: %v", err)
			}

			testMcpServer := getMCPServerFromFile(t, configPath, tt.mcpServerKeyPath+"."+tt.mcpServerName)

			assert.Equal(t, mcpServer.Url, testMcpServer.Url, "The retrieved value should match the set value")

			t.Cleanup(func() {
				if err := os.RemoveAll(tempDir); err != nil {
					t.Logf("Failed to remove temp dir: %v", err)
				}
			})
		})
	}

	// Run subtests

	for _, tt := range tests {

		t.Run("UpdateExistingMCPServer", func(t *testing.T) {
			t.Parallel()

			uniqueId := uuid.New().String()
			tempDir, configPath := setupEmptyTestConfig(t, uniqueId)

			jsu := JSONConfigUpdater{
				Path:                 configPath,
				MCPServersPathPrefix: tt.mcpServerPatchPath,
			}

			// add an MCP server so we can update it
			mcpServer := MCPServer{
				Url: fmt.Sprintf("test-url-%s-before-update", uniqueId),
			}
			err := jsu.Upsert(tt.mcpServerName, mcpServer)
			if err != nil {
				t.Fatalf("Failed to add mcp server to config: %v", err)
			}
			testMcpServer := getMCPServerFromFile(t, configPath, tt.mcpServerKeyPath+"."+tt.mcpServerName)
			assert.Equal(t, mcpServer.Url, testMcpServer.Url, "The retrieved value should match the set value")

			// now we update the mcp server
			mcpServerUpdated := MCPServer{
				Url: fmt.Sprintf("test-url-%s-after-update", uniqueId),
			}
			err = jsu.Upsert(tt.mcpServerName, mcpServerUpdated)
			if err != nil {
				t.Fatalf("Failed to update mcp server inconfig: %v", err)
			}
			// we make sure to get the same mcp server that we created and then updated
			testMcpServerUpdate := getMCPServerFromFile(t, configPath, tt.mcpServerKeyPath+"."+tt.mcpServerName)
			assert.Equal(t, mcpServerUpdated.Url, testMcpServerUpdate.Url, "The retrieved value should match the set value")

			if err != nil {
				t.Fatalf("Failed to update config: %v", err)
			}

			t.Cleanup(func() {
				if err := os.RemoveAll(tempDir); err != nil {
					t.Logf("Failed to remove temp dir: %v", err)
				}
			})
		})
	}
}

func TestRemoveMCPServerConfigNew(t *testing.T) {
	t.Parallel()

	logger.Initialize()

	tests := []struct {
		mcpServerPatchPath string // the path used by the patch operation
		mcpServerKeyPath   string // the path used to retrieve the value from the config file (for testing purposes)
		mcpServerName      string // the name of the MCP server to remove
	}{
		{mcpServerPatchPath: "/mcp/servers", mcpServerKeyPath: "mcp.servers", mcpServerName: "testMcpServerRemove"},
		{mcpServerPatchPath: "/mcpServers", mcpServerKeyPath: "mcpServers", mcpServerName: "testMcpServerRemove"},
	}

	for _, tt := range tests {
		t.Run("DeleteMCPServer", func(t *testing.T) {
			t.Parallel()

			uniqueId := uuid.New().String()
			tempDir, configPath := setupEmptyTestConfig(t, uniqueId)

			jsu := JSONConfigUpdater{
				Path:                 configPath,
				MCPServersPathPrefix: tt.mcpServerPatchPath,
			}

			// add an MCP server so we can remove it
			mcpServer := MCPServer{
				Url: fmt.Sprintf("test-url-%s-before-removal", uniqueId),
			}
			err := jsu.Upsert(tt.mcpServerName, mcpServer)
			if err != nil {
				t.Fatalf("Failed to add mcp server to config: %v", err)
			}
			testMcpServer := getMCPServerFromFile(t, configPath, tt.mcpServerKeyPath+"."+tt.mcpServerName)
			assert.Equal(t, mcpServer.Url, testMcpServer.Url, "The retrieved value should match the set value")

			// remove both mcp servers
			err = jsu.Remove(tt.mcpServerName)
			if err != nil {
				t.Fatalf("Failed to remove mcp server testMcpServer from config: %v", err)
			}

			// read the config file and check that the mcp servers are removed
			content, err := os.ReadFile(configPath)
			if err != nil {
				log.Fatalf("Failed to read file: %v", err)
			}

			testMcpServerJson := gjson.GetBytes(content, tt.mcpServerKeyPath+"."+tt.mcpServerName).Raw
			if testMcpServerJson != "" {
				t.Fatalf("Failed to remove mcp server testMcpServer from config: %v", testMcpServerJson)
			}

			t.Cleanup(func() {
				if err := os.RemoveAll(tempDir); err != nil {
					t.Logf("Failed to remove temp dir: %v", err)
				}
			})
		})
	}
}

// setupEmptyTestConfig creates a temporary directory and an empty config file for testing
// It returns the temp directory path, config file path, and the loaded config
// The logs are created in "/var/folders/2k/jvn73p4d2nn_j6tvc40vj4r00000gn/T/toolhive-test4175700918/config-9f74ab6d-0b4e-4956-b818-315bf16aa803.json"
func setupEmptyTestConfig(t *testing.T, testName string) (string, string) {
	t.Helper()

	// Create a temporary file
	tempDir, err := os.MkdirTemp("", "toolhive-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create a test config file with existing MCP servers
	configPath := filepath.Join(tempDir, fmt.Sprintf("config-%s.json", testName))
	testConfig := map[string]interface{}{}

	// // Write the test config to the file
	data, err := json.MarshalIndent(testConfig, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	return tempDir, configPath
}

// getMCPServerFromFile reads the config file and returns a mcpServer object
func getMCPServerFromFile(t *testing.T, configPath string, key string) MCPServer {
	t.Helper()

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	testMcpServerJson := gjson.GetBytes(content, key).Raw

	var testMcpServer MCPServer
	err = json.Unmarshal([]byte(testMcpServerJson), &testMcpServer)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	return testMcpServer
}

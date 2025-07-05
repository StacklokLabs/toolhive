package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/tailscale/hujson"
	"github.com/tidwall/gjson"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// ConfigUpdater defines the interface for types which can edit MCP client config files.
type ConfigUpdater interface {
	Upsert(serverName string, data MCPServer) error
	Remove(serverName string) error
}

// MCPServer represents an MCP server in a MCP client config file
type MCPServer struct {
	Url  string `json:"url,omitempty"`
	Type string `json:"type,omitempty"`
}

// JSONConfigUpdater is a ConfigUpdater that is responsible for updating
// JSON config files.
type JSONConfigUpdater struct {
	Path                 string
	MCPServersPathPrefix string
}

// Upsert inserts or updates an MCP server in the MCP client config file
func (jcu *JSONConfigUpdater) Upsert(serverName string, data MCPServer) error {
	// Create a lock file
	fileLock := flock.New(jcu.Path + ".lock")

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	// Try to acquire the lock with a timeout
	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("failed to acquire lock: timeout after %v", lockTimeout)
	}
	defer fileLock.Unlock()

	content, err := os.ReadFile(jcu.Path)
	if err != nil {
		logger.Errorf("Failed to read file: %v", err)
	}

	content = ensurePathExists(content, jcu.MCPServersPathPrefix)

	v, _ := hujson.Parse(content)

	dataJSON, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("Unable to marshal the MCPServer into JSON: %v", err)
	}

	patch := fmt.Sprintf(`[{ "op": "add", "path": "%s/%s", "value": %s } ]`, jcu.MCPServersPathPrefix, serverName, dataJSON)
	err = v.Patch([]byte(patch))
	if err != nil {
		logger.Errorf("Failed to patch file: %v", err)
	}

	formatted, _ := hujson.Format(v.Pack())
	if err != nil {
		logger.Errorf("Failed to format the patched file: %v", err)
	}

	// Write back to the file
	if err := os.WriteFile(jcu.Path, formatted, 0600); err != nil {
		logger.Errorf("Failed to write file: %v", err)
	}

	logger.Infof("Successfully updated the client config file for MCPServer %s", serverName)

	return nil
}

// Remove removes an MCP server from the MCP client config file
func (jcu *JSONConfigUpdater) Remove(serverName string) error {
	// Create a lock file
	fileLock := flock.New(jcu.Path + ".lock")

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	// Try to acquire the lock with a timeout
	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("failed to acquire lock: timeout after %v", lockTimeout)
	}
	defer fileLock.Unlock()

	content, err := os.ReadFile(jcu.Path)
	if err != nil {
		logger.Errorf("Failed to read file: %v", err)
	}

	v, _ := hujson.Parse(content)

	patch := fmt.Sprintf(`[{ "op": "remove", "path": "%s/%s" } ]`, jcu.MCPServersPathPrefix, serverName)
	err = v.Patch([]byte(patch))
	if err != nil {
		logger.Errorf("Failed to patch file: %v", err)
	}

	formatted, _ := hujson.Format(v.Pack())

	// Write back to the file
	if err := os.WriteFile(jcu.Path, formatted, 0600); err != nil {
		logger.Errorf("Failed to write file: %v", err)
	}

	logger.Infof("Successfully removed the MCPServer %s from the client config file", serverName)

	return nil
}

// ensurePathExists ensures that the path exists in the JSON content
// and returns the updated content.
// For example:
//   - if the path is "/mcp/servers",
//     the function will ensure that the path "/mcp/servers" exists
//     and returns the updated content.
//   - if the path is "/mcpServers",
//     the function will ensure that the path "/mcpServers" exists
//     and returns the updated content.
//
// This is necessary because the MCP client config file is a JSON object,
// and we need to ensure that the path exists before we can add a new key to it.
func ensurePathExists(content []byte, path string) []byte {
	segments := strings.Split(path, "/")

	// Navigate through the JSON structure
	var pathSoFarForPatch string
	var pathSoFarForRetrieval string
	for i, segment := range segments[:] {
		// we want to skip the first segments because it is the root
		if path[0] == '/' && (i == 0) {
			continue
		}

		// We build the path up to this segment so that we can check if it exists
		// and if it doesn't, we can create it as an empty object.
		// The "/" is added to the path for the patch operation because the path
		// is a JSON pointer, and JSON pointers are prefixed with "/".
		// The "." is added to the path for the retrieval operation.
		if len(pathSoFarForPatch) == 0 {
			pathSoFarForPatch = "/" + segment
			pathSoFarForRetrieval = segment
		} else {
			pathSoFarForPatch = pathSoFarForPatch + "/" + segment
			pathSoFarForRetrieval = pathSoFarForRetrieval + "." + segment
		}

		// We retrieve the segment from the content so that we can check if it exists
		// and if it doesn't, we can create it as an empty object. If it does exist,
		// we can skip the patch operation onto the next segment.
		segmentPath := gjson.GetBytes(content, pathSoFarForRetrieval).Raw
		if segmentPath != "" {
			continue
		}

		// Create a JSON patch to add an empty object at this path
		patch := fmt.Sprintf(`[{ "op": "add", "path": "%s", "value": {} }]`, pathSoFarForPatch)

		// Parse the current content and apply the patch
		v, _ := hujson.Parse(content)
		err := v.Patch([]byte(patch))
		if err != nil {
			logger.Errorf("Failed to patch file: %v", err)
		}

		// Update the content with the patched version
		content = v.Pack()
	}
	// Parse the updated content with hujson to maintain formatting
	v, _ := hujson.Parse(content)
	formatted, _ := hujson.Format(v.Pack())
	return formatted
}

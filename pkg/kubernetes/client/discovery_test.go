package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/config"
)

func TestGetClientStatus(t *testing.T) {
	// Setup a temporary home directory for testing
	origHome := os.Getenv("HOME")
	tempHome, err := os.MkdirTemp("", "toolhive-test-home")
	require.NoError(t, err)
	defer os.RemoveAll(tempHome)

	t.Setenv("HOME", tempHome)
	defer t.Setenv("HOME", origHome)

	// Create mock config with registered clients
	mockConfig := &config.Config{
		Clients: config.Clients{
			RegisteredClients: []string{string(ClaudeCode)},
		},
	}
	cleanup := MockConfig(t, mockConfig)
	defer cleanup()

	// Create a mock Cursor config file
	_, err = os.Create(filepath.Join(tempHome, ".cursor"))
	require.NoError(t, err)

	// Create a mock ClaudeCode config file
	_, err = os.Create(filepath.Join(tempHome, ".claude.json"))
	require.NoError(t, err)

	statuses, err := GetClientStatus()
	require.NoError(t, err)
	require.NotNil(t, statuses)

	// Create a map for easier testing
	statusMap := make(map[MCPClient]MCPClientStatus)
	for _, status := range statuses {
		statusMap[status.ClientType] = status
	}

	claudeStatus, exists := statusMap[ClaudeCode]
	assert.True(t, exists)
	assert.True(t, claudeStatus.Installed)
	assert.True(t, claudeStatus.Registered)

	cursorStatus, exists := statusMap[Cursor]
	assert.True(t, exists)
	assert.True(t, cursorStatus.Installed)
	assert.False(t, cursorStatus.Registered)

	vscodeStatus, exists := statusMap[VSCode]
	assert.True(t, exists)
	assert.False(t, vscodeStatus.Installed)
	assert.False(t, vscodeStatus.Registered)
}

package labels

import (
	"testing"
)

func TestAddStandardLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		containerName     string
		containerBaseName string
		transportType     string
		port              int
		expected          map[string]string
	}{
		{
			name:              "Standard labels",
			containerName:     "test-container",
			containerBaseName: "test-base",
			transportType:     "http",
			port:              8080,
			expected: map[string]string{
				LabelEnabled:   "true",
				LabelName:      "test-container",
				LabelBaseName:  "test-base",
				LabelTransport: "http",
				LabelPort:      "8080",
				LabelToolType:  "mcp",
			},
		},
		{
			name:              "Different port",
			containerName:     "another-container",
			containerBaseName: "another-base",
			transportType:     "https",
			port:              9090,
			expected: map[string]string{
				LabelEnabled:   "true",
				LabelName:      "another-container",
				LabelBaseName:  "another-base",
				LabelTransport: "https",
				LabelPort:      "9090",
				LabelToolType:  "mcp",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			labels := make(map[string]string)
			AddStandardLabels(labels, tc.containerName, tc.containerBaseName, tc.transportType, tc.port)

			// Verify all expected labels are present with correct values
			for key, expectedValue := range tc.expected {
				actualValue, exists := labels[key]
				if !exists {
					t.Errorf("Expected label %s to exist, but it doesn't", key)
				}
				if actualValue != expectedValue {
					t.Errorf("Expected label %s to be %s, but got %s", key, expectedValue, actualValue)
				}
			}

			// Verify no unexpected labels are present
			if len(labels) != len(tc.expected) {
				t.Errorf("Expected %d labels, but got %d", len(tc.expected), len(labels))
			}
		})
	}
}

func TestFormatToolHiveFilter(t *testing.T) {
	t.Parallel()
	expected := "toolhive=true"
	result := FormatToolHiveFilter()
	if result != expected {
		t.Errorf("Expected filter to be %s, but got %s", expected, result)
	}
}

func TestIsToolHiveContainer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{
			name: "Valid ToolHive container",
			labels: map[string]string{
				LabelEnabled: "true",
			},
			expected: true,
		},
		{
			name: "Valid ToolHive container with uppercase TRUE",
			labels: map[string]string{
				LabelEnabled: "TRUE",
			},
			expected: true,
		},
		{
			name: "Valid ToolHive container with mixed case TrUe",
			labels: map[string]string{
				LabelEnabled: "TrUe",
			},
			expected: true,
		},
		{
			name: "Not a ToolHive container - false value",
			labels: map[string]string{
				LabelEnabled: "false",
			},
			expected: false,
		},
		{
			name: "Not a ToolHive container - other value",
			labels: map[string]string{
				LabelEnabled: "yes",
			},
			expected: false,
		},
		{
			name:     "Not a ToolHive container - empty labels",
			labels:   map[string]string{},
			expected: false,
		},
		{
			name: "Not a ToolHive container - label missing",
			labels: map[string]string{
				"some-other-label": "value",
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := IsToolHiveContainer(tc.labels)
			if result != tc.expected {
				t.Errorf("Expected IsToolHiveContainer to return %v, but got %v", tc.expected, result)
			}
		})
	}
}

func TestGetContainerName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "Container name exists",
			labels: map[string]string{
				LabelName: "test-container",
			},
			expected: "test-container",
		},
		{
			name:     "Container name doesn't exist",
			labels:   map[string]string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := GetContainerName(tc.labels)
			if result != tc.expected {
				t.Errorf("Expected container name to be %s, but got %s", tc.expected, result)
			}
		})
	}
}

func TestGetContainerBaseName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "Container base name exists",
			labels: map[string]string{
				LabelBaseName: "test-base",
			},
			expected: "test-base",
		},
		{
			name:     "Container base name doesn't exist",
			labels:   map[string]string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := GetContainerBaseName(tc.labels)
			if result != tc.expected {
				t.Errorf("Expected container base name to be %s, but got %s", tc.expected, result)
			}
		})
	}
}

func TestGetTransportType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "Transport type exists",
			labels: map[string]string{
				LabelTransport: "http",
			},
			expected: "http",
		},
		{
			name:     "Transport type doesn't exist",
			labels:   map[string]string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := GetTransportType(tc.labels)
			if result != tc.expected {
				t.Errorf("Expected transport type to be %s, but got %s", tc.expected, result)
			}
		})
	}
}

func TestGetPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		labels      map[string]string
		expected    int
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid port",
			labels: map[string]string{
				LabelPort: "8080",
			},
			expected:    8080,
			expectError: false,
		},
		{
			name:        "Port label missing",
			labels:      map[string]string{},
			expected:    0,
			expectError: true,
			errorMsg:    "port label not found",
		},
		{
			name: "Invalid port - not a number",
			labels: map[string]string{
				LabelPort: "not-a-number",
			},
			expected:    0,
			expectError: true,
			errorMsg:    "invalid port: not-a-number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := GetPort(tc.labels)

			// Check error
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				} else if err.Error() != tc.errorMsg {
					t.Errorf("Expected error message '%s', but got '%s'", tc.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}

			// Check result
			if result != tc.expected {
				t.Errorf("Expected port to be %d, but got %d", tc.expected, result)
			}
		})
	}
}

func TestGetToolType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "Tool type exists",
			labels: map[string]string{
				LabelToolType: "mcp",
			},
			expected: "mcp",
		},
		{
			name:     "Tool type doesn't exist",
			labels:   map[string]string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := GetToolType(tc.labels)
			if result != tc.expected {
				t.Errorf("Expected tool type to be %s, but got %s", tc.expected, result)
			}
		})
	}
}

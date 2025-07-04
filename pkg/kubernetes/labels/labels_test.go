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

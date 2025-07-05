package updates

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewVersionClientWithSuffix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		suffix   string
		expected string
	}{
		{
			name:     "no suffix",
			suffix:   "",
			expected: "",
		},
		{
			name:     "operator suffix",
			suffix:   "operator",
			expected: "operator",
		},
		{
			name:     "custom suffix",
			suffix:   "custom",
			expected: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := NewVersionClientWithSuffix(tt.suffix)
			defaultClient, ok := client.(*defaultVersionClient)
			assert.True(t, ok, "Expected defaultVersionClient type")
			assert.Equal(t, tt.expected, defaultClient.userAgentSuffix)
		})
	}
}

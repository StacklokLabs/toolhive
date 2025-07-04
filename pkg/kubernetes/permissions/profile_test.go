package permissions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMountDeclaration_Parse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		declaration    MountDeclaration
		expectedSource string
		expectedTarget string
		expectError    bool
	}{
		{
			name:           "Single path",
			declaration:    "/path/to/dir",
			expectedSource: "/path/to/dir",
			expectedTarget: "/path/to/dir",
			expectError:    false,
		},
		{
			name:           "Host path to container path",
			declaration:    "/host/path:/container/path",
			expectedSource: "/host/path",
			expectedTarget: "/container/path",
			expectError:    false,
		},
		{
			name:           "Resource URI",
			declaration:    "volume://myvolume:/container/path",
			expectedSource: "volume://myvolume",
			expectedTarget: "/container/path",
			expectError:    false,
		},
		{
			name:           "Different resource URI",
			declaration:    "secret://mysecret:/container/path",
			expectedSource: "secret://mysecret",
			expectedTarget: "/container/path",
			expectError:    false,
		},
		// Security-focused tests
		{
			name:           "Path with spaces",
			declaration:    "/path with spaces:/container/path",
			expectedSource: "/path with spaces",
			expectedTarget: "/container/path",
			expectError:    false,
		},
		{
			name:           "Path with special characters",
			declaration:    "/path/with/special/chars!@#:/container/path",
			expectedSource: "/path/with/special/chars!@#",
			expectedTarget: "/container/path",
			expectError:    false,
		},
		{
			name:           "Path with Unicode characters",
			declaration:    "/path/with/unicode/😀:/container/path",
			expectedSource: "/path/with/unicode/😀",
			expectedTarget: "/container/path",
			expectError:    false,
		},
		{
			name:           "Path with potential command injection",
			declaration:    "/path/with/$(rm -rf *):/container/path",
			expectedSource: "",
			expectedTarget: "",
			expectError:    true, // Now expecting an error due to validation
		},
		{
			name:           "Path with potential path traversal",
			declaration:    "/path/with/../../../etc/passwd:/container/path",
			expectedSource: "/etc/passwd", // filepath.Clean resolves the path
			expectedTarget: "/container/path",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source, target, err := tt.declaration.Parse()

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedSource, source)
			assert.Equal(t, tt.expectedTarget, target)
		})
	}
}

func TestMountDeclaration_IsValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		declaration MountDeclaration
		expected    bool
	}{
		{
			name:        "Single path",
			declaration: "/path/to/dir",
			expected:    true,
		},
		{
			name:        "Host path to container path",
			declaration: "/host/path:/container/path",
			expected:    true,
		},
		{
			name:        "Resource URI",
			declaration: "volume://myvolume:/container/path",
			expected:    true,
		},
		{
			name:        "Empty string",
			declaration: "",
			expected:    true, // Empty string is treated as a single path
		},
		// Security-focused tests
		{
			name:        "Path with potential command injection",
			declaration: "/path/with/$(rm -rf *):/container/path",
			expected:    false, // Now invalid due to validation
		},
		{
			name:        "Path with potential path traversal",
			declaration: "/path/with/../../../etc/passwd:/container/path",
			expected:    true, // Valid format, but potentially dangerous
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.declaration.IsValid())
		})
	}
}

func TestMountDeclaration_IsResourceURI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		declaration MountDeclaration
		expected    bool
	}{
		{
			name:        "Single path",
			declaration: "/path/to/dir",
			expected:    false,
		},
		{
			name:        "Host path to container path",
			declaration: "/host/path:/container/path",
			expected:    false,
		},
		{
			name:        "Resource URI",
			declaration: "volume://myvolume:/container/path",
			expected:    true,
		},
		{
			name:        "Different resource URI",
			declaration: "secret://mysecret:/container/path",
			expected:    true,
		},
		// Security-focused tests
		{
			name:        "Malformed resource URI",
			declaration: "volume:/myvolume:/container/path", // Missing a slash
			expected:    false,
		},
		{
			name:        "Resource URI with potential command injection",
			declaration: "volume://$(rm -rf *):/container/path",
			expected:    true, // Valid format, but potentially dangerous
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.declaration.IsResourceURI())
		})
	}
}

func TestMountDeclaration_GetResourceType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		declaration  MountDeclaration
		expectedType string
		expectError  bool
	}{
		{
			name:         "Single path",
			declaration:  "/path/to/dir",
			expectedType: "",
			expectError:  true,
		},
		{
			name:         "Host path to container path",
			declaration:  "/host/path:/container/path",
			expectedType: "",
			expectError:  true,
		},
		{
			name:         "Volume resource URI",
			declaration:  "volume://myvolume:/container/path",
			expectedType: "volume",
			expectError:  false,
		},
		{
			name:         "Secret resource URI",
			declaration:  "secret://mysecret:/container/path",
			expectedType: "secret",
			expectError:  false,
		},
		// Security-focused tests
		{
			name:         "Resource URI with potential command injection",
			declaration:  "volume://$(rm -rf *):/container/path",
			expectedType: "volume",
			expectError:  false, // Valid format, but potentially dangerous
		},
		{
			name:         "Resource URI with unusual scheme",
			declaration:  "file://etc/passwd:/container/path",
			expectedType: "file",
			expectError:  false, // Valid format, but potentially dangerous
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resourceType, err := tt.declaration.GetResourceType()

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedType, resourceType)
		})
	}
}

func TestParseMountDeclarations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		declarations []string
		expectError  bool
	}{
		{
			name: "Valid declarations",
			declarations: []string{
				"/path/to/dir",
				"/host/path:/container/path",
				"volume://myvolume:/container/path",
			},
			expectError: false,
		},
		{
			name:         "Empty list",
			declarations: []string{},
			expectError:  false,
		},
		// Security-focused tests
		{
			name: "Declarations with potential security issues",
			declarations: []string{
				"/path/with/$(rm -rf *):/container/path",
				"volume://$(rm -rf *):/container/path",
				"/path/with/../../../etc/passwd:/container/path",
			},
			expectError: true, // Now expecting an error due to validation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mounts, err := ParseMountDeclarations(tt.declarations)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, len(tt.declarations), len(mounts))
		})
	}
}

// Additional security-focused tests

func TestMountDeclaration_SecurityValidation(t *testing.T) {
	t.Parallel()
	// These tests check that our parsing is robust against various security issues

	// Test for path traversal - this should be cleaned but allowed
	traversalMount := MountDeclaration("/etc/passwd:/container/path")
	source, target, err := traversalMount.Parse()
	require.NoError(t, err)
	assert.Equal(t, "/etc/passwd", source)
	assert.Equal(t, "/container/path", target)

	// Test for command injection - this should be rejected
	injectionMount := MountDeclaration("$(rm -rf *):/container/path")
	_, _, err = injectionMount.Parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "potential command injection")

	// Test for null byte injection - this should be rejected
	nullByteMount := MountDeclaration("/path/with/null\x00byte:/container/path")
	_, _, err = nullByteMount.Parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null byte detected")
}

func TestMountDeclaration_EdgeCases(t *testing.T) {
	t.Parallel()
	// Test with multiple colons - should fail with a clear error message
	multipleColons := MountDeclaration("/path:with:multiple:colons:/container/path")
	_, _, err := multipleColons.Parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mount declaration format")

	// Test with very long paths
	longPath := "/very/long/path/" + strings.Repeat("a", 1000)
	longMount := MountDeclaration(longPath + ":/container/path")
	source, target, err := longMount.Parse()
	require.NoError(t, err)
	assert.Equal(t, longPath, source)
	assert.Equal(t, "/container/path", target)

	// Test with path containing "://" but not at the beginning
	pathWithColon := MountDeclaration("/some/other/path/://:/tmp/foo")
	_, _, err = pathWithColon.Parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mount declaration format")
}

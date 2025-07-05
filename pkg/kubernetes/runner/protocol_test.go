package runner

import (
	"testing"

	"github.com/stacklok/toolhive/pkg/kubernetes/container/templates"
)

func TestIsLocalGoPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "current directory",
			path:     ".",
			expected: true,
		},
		{
			name:     "relative path with ./",
			path:     "./cmd/server",
			expected: true,
		},
		{
			name:     "relative path with ../",
			path:     "../other-project",
			expected: true,
		},
		{
			name:     "absolute path",
			path:     "/home/user/project",
			expected: true,
		},
		{
			name:     "remote package",
			path:     "github.com/example/package",
			expected: false,
		},
		{
			name:     "remote package with version",
			path:     "github.com/example/package@v1.0.0",
			expected: false,
		},
		{
			name:     "simple package name",
			path:     "mypackage",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isLocalGoPath(tt.path)
			if result != tt.expected {
				t.Errorf("isLocalGoPath(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestPackageNameToImageName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple package name",
			input:    "mypackage",
			expected: "mypackage",
		},
		{
			name:     "package with slashes",
			input:    "github.com/user/repo",
			expected: "github-com-user-repo",
		},
		{
			name:     "package with version",
			input:    "github.com/user/repo@v1.0.0",
			expected: "github-com-user-repo-v1-0-0",
		},
		{
			name:     "relative path with ./",
			input:    "./cmd/server",
			expected: "cmd-server",
		},
		{
			name:     "relative path with ../",
			input:    "../other-project",
			expected: "other-project",
		},
		{
			name:     "current directory",
			input:    ".",
			expected: "toolhive-container",
		},
		{
			name:     "path with dots",
			input:    "./my.project",
			expected: "my-project",
		},
		{
			name:     "complex path",
			input:    "./cmd/my.server/main",
			expected: "cmd-my-server-main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := packageNameToImageName(tt.input)
			if result != tt.expected {
				t.Errorf("packageNameToImageName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsImageProtocolScheme(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "uvx scheme",
			input:    "uvx://package-name",
			expected: true,
		},
		{
			name:     "npx scheme",
			input:    "npx://package-name",
			expected: true,
		},
		{
			name:     "go scheme",
			input:    "go://package-name",
			expected: true,
		},
		{
			name:     "go scheme with local path",
			input:    "go://./cmd/server",
			expected: true,
		},
		{
			name:     "regular image name",
			input:    "docker.io/library/alpine:latest",
			expected: false,
		},
		{
			name:     "registry server name",
			input:    "fetch",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := IsImageProtocolScheme(tt.input)
			if result != tt.expected {
				t.Errorf("IsImageProtocolScheme(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestTemplateDataWithLocalPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		packageName string
		expected    templates.TemplateData
	}{
		{
			name:        "remote package",
			packageName: "github.com/example/package",
			expected: templates.TemplateData{
				MCPPackage:  "github.com/example/package",
				MCPArgs:     []string{},
				IsLocalPath: false,
			},
		},
		{
			name:        "local relative path",
			packageName: "./cmd/server",
			expected: templates.TemplateData{
				MCPPackage:  "./cmd/server",
				MCPArgs:     []string{},
				IsLocalPath: true,
			},
		},
		{
			name:        "current directory",
			packageName: ".",
			expected: templates.TemplateData{
				MCPPackage:  ".",
				MCPArgs:     []string{},
				IsLocalPath: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Test the logic that would be used in HandleProtocolScheme
			isLocalPath := isLocalGoPath(tt.packageName)

			templateData := templates.TemplateData{
				MCPPackage:  tt.packageName,
				MCPArgs:     []string{},
				IsLocalPath: isLocalPath,
			}

			if templateData.MCPPackage != tt.expected.MCPPackage {
				t.Errorf("MCPPackage = %q, want %q", templateData.MCPPackage, tt.expected.MCPPackage)
			}
			if templateData.IsLocalPath != tt.expected.IsLocalPath {
				t.Errorf("IsLocalPath = %v, want %v", templateData.IsLocalPath, tt.expected.IsLocalPath)
			}
		})
	}
}

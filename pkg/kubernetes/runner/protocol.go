package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/certs"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/images"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/templates"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// Protocol schemes
const (
	UVXScheme = "uvx://"
	NPXScheme = "npx://"
	GOScheme  = "go://"
)

// HandleProtocolScheme checks if the serverOrImage string contains a protocol scheme (uvx://, npx://, or go://)
// and builds a Docker image for it if needed.
// Returns the Docker image name to use and any error encountered.
func HandleProtocolScheme(
	ctx context.Context,
	imageManager images.ImageManager,
	serverOrImage string,
	caCertPath string,
) (string, error) {
	transportType, packageName, err := parseProtocolScheme(serverOrImage)
	if err != nil {
		return "", err
	}

	templateData, err := createTemplateData(transportType, packageName, caCertPath)
	if err != nil {
		return "", err
	}

	return buildImageFromTemplate(ctx, imageManager, transportType, packageName, templateData)
}

// parseProtocolScheme extracts the transport type and package name from the protocol scheme.
func parseProtocolScheme(serverOrImage string) (templates.TransportType, string, error) {
	if strings.HasPrefix(serverOrImage, UVXScheme) {
		return templates.TransportTypeUVX, strings.TrimPrefix(serverOrImage, UVXScheme), nil
	}
	if strings.HasPrefix(serverOrImage, NPXScheme) {
		return templates.TransportTypeNPX, strings.TrimPrefix(serverOrImage, NPXScheme), nil
	}
	if strings.HasPrefix(serverOrImage, GOScheme) {
		return templates.TransportTypeGO, strings.TrimPrefix(serverOrImage, GOScheme), nil
	}
	return "", "", fmt.Errorf("unsupported protocol scheme: %s", serverOrImage)
}

// createTemplateData creates the template data with optional CA certificate.
func createTemplateData(transportType templates.TransportType, packageName, caCertPath string) (templates.TemplateData, error) {
	// Check if this is a local path (for Go packages only)
	isLocalPath := transportType == templates.TransportTypeGO && isLocalGoPath(packageName)

	templateData := templates.TemplateData{
		MCPPackage:  packageName,
		MCPArgs:     []string{}, // No additional arguments for now
		IsLocalPath: isLocalPath,
	}

	if caCertPath != "" {
		if err := addCACertToTemplate(caCertPath, &templateData); err != nil {
			return templateData, err
		}
	}

	return templateData, nil
}

// addCACertToTemplate reads and validates a CA certificate, adding it to the template data.
func addCACertToTemplate(caCertPath string, templateData *templates.TemplateData) error {
	logger.Debugf("Using custom CA certificate from: %s", caCertPath)

	// Read the CA certificate file
	// #nosec G304 -- This is a user-provided file path that we need to read
	caCertContent, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate file: %w", err)
	}

	// Validate that the file contains a valid PEM certificate
	if err := certs.ValidateCACertificate(caCertContent); err != nil {
		return fmt.Errorf("invalid CA certificate: %w", err)
	}

	// Add the CA certificate content to the template data
	templateData.CACertContent = string(caCertContent)
	logger.Debugf("Successfully validated and loaded CA certificate")
	return nil
}

// buildContext represents a Docker build context with cleanup functionality.
type buildContext struct {
	Dir            string
	DockerfilePath string
	CleanupFunc    func()
}

// setupBuildContext sets up the appropriate build context directory based on whether
// we're dealing with a local path or remote package.
func setupBuildContext(packageName string, isLocalPath bool) (*buildContext, error) {
	if isLocalPath {
		return setupLocalBuildContext(packageName)
	}
	return setupTempBuildContext()
}

// setupLocalBuildContext sets up a build context using the local directory directly.
func setupLocalBuildContext(packageName string) (*buildContext, error) {
	absPath, err := filepath.Abs(packageName)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", packageName, err)
	}

	// Check if the source path exists
	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("source path does not exist: %s: %w", absPath, err)
	}

	// For Go projects, use the current working directory as the build context
	// to ensure go.mod and the entire project structure is available
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	dockerfilePath := filepath.Join(currentDir, "Dockerfile")

	logger.Debugf("Using current working directory as build context: %s", currentDir)

	return &buildContext{
		Dir:            currentDir,
		DockerfilePath: dockerfilePath,
		CleanupFunc: func() {
			// Clean up the temporary Dockerfile only if we created it
			if _, err := os.Stat(dockerfilePath); err == nil {
				// Check if this is our generated Dockerfile by reading the first few lines
				// #nosec G304 -- This is a controlled file read operation for cleanup verification
				if content, readErr := os.ReadFile(dockerfilePath); readErr == nil {
					if strings.Contains(string(content), "# Generated by ToolHive") {
						if err := os.Remove(dockerfilePath); err != nil {
							logger.Debugf("Failed to remove temporary Dockerfile: %v", err)
						}
					}
				}
			}
		},
	}, nil
}

// setupTempBuildContext sets up a temporary build context directory.
func setupTempBuildContext() (*buildContext, error) {
	tempDir, err := os.MkdirTemp("", "toolhive-docker-build-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}

	dockerfilePath := filepath.Join(tempDir, "Dockerfile")

	logger.Debugf("Using temporary directory as build context: %s", tempDir)

	return &buildContext{
		Dir:            tempDir,
		DockerfilePath: dockerfilePath,
		CleanupFunc: func() {
			if err := os.RemoveAll(tempDir); err != nil {
				logger.Debugf("Failed to remove temporary directory: %v", err)
			}
		},
	}, nil
}

// writeDockerfile writes the Dockerfile content to the build context.
// For local paths, it checks if a Dockerfile already exists and avoids overwriting it.
func writeDockerfile(dockerfilePath, dockerfileContent string, isLocalPath bool) error {
	if isLocalPath {
		// Check if a Dockerfile already exists
		if _, err := os.Stat(dockerfilePath); err == nil {
			logger.Infof("Dockerfile already exists at %s, using existing Dockerfile", dockerfilePath)
			return nil // Use the existing Dockerfile
		}
	}

	// Add a comment marker to identify our generated Dockerfile
	markedContent := "# Generated by ToolHive - temporary file\n" + dockerfileContent

	if err := os.WriteFile(dockerfilePath, []byte(markedContent), 0600); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	if isLocalPath {
		logger.Debugf("Created temporary Dockerfile at %s", dockerfilePath)
	}

	return nil
}

// writeCACertificate writes the CA certificate to the build context if provided.
func writeCACertificate(buildContextDir, caCertContent string, isLocalPath bool) (func(), error) {
	if caCertContent == "" {
		return func() {}, nil
	}

	caCertFilePath := filepath.Join(buildContextDir, "ca-cert.crt")
	if err := os.WriteFile(caCertFilePath, []byte(caCertContent), 0600); err != nil {
		return nil, fmt.Errorf("failed to write CA certificate file: %w", err)
	}

	logger.Debugf("Added CA certificate to build context: %s", caCertFilePath)

	var cleanupFunc func()
	if isLocalPath {
		// For local paths, clean up the CA certificate file after build
		cleanupFunc = func() {
			if err := os.Remove(caCertFilePath); err != nil {
				logger.Debugf("Failed to remove temporary CA certificate: %v", err)
			}
		}
	} else {
		// For temp directories, no specific cleanup needed (handled by build context cleanup)
		cleanupFunc = func() {}
	}

	return cleanupFunc, nil
}

// generateImageName generates a unique Docker image name based on the package and transport type.
func generateImageName(transportType templates.TransportType, packageName string) string {
	tag := time.Now().Format("20060102150405")
	return strings.ToLower(fmt.Sprintf("toolhivelocal/%s-%s:%s",
		string(transportType),
		packageNameToImageName(packageName),
		tag))
}

// buildImageFromTemplate builds a Docker image from the template data.
func buildImageFromTemplate(
	ctx context.Context,
	imageManager images.ImageManager,
	transportType templates.TransportType,
	packageName string,
	templateData templates.TemplateData,
) (string, error) {

	// Get the Dockerfile content
	dockerfileContent, err := templates.GetDockerfileTemplate(transportType, templateData)
	if err != nil {
		return "", fmt.Errorf("failed to get Dockerfile template: %w", err)
	}

	// Set up the build context
	buildCtx, err := setupBuildContext(packageName, templateData.IsLocalPath)
	if err != nil {
		return "", err
	}
	defer buildCtx.CleanupFunc()

	// Write the Dockerfile
	if err := writeDockerfile(buildCtx.DockerfilePath, dockerfileContent, templateData.IsLocalPath); err != nil {
		return "", err
	}

	// Write CA certificate if provided
	caCertCleanup, err := writeCACertificate(buildCtx.Dir, templateData.CACertContent, templateData.IsLocalPath)
	if err != nil {
		return "", err
	}
	defer caCertCleanup()

	// Generate image name
	imageName := generateImageName(transportType, packageName)

	// Log the build process
	logger.Debugf("Building Docker image for %s package: %s", transportType, packageName)
	logger.Debugf("Using Dockerfile:\n%s", dockerfileContent)

	// Build the Docker image
	logger.Infof("Building Docker image for %s package: %s", transportType, packageName)
	if err := imageManager.BuildImage(ctx, buildCtx.Dir, imageName); err != nil {
		return "", fmt.Errorf("failed to build Docker image: %w", err)
	}
	logger.Infof("Successfully built Docker image: %s", imageName)

	return imageName, nil
}

// Replace slashes with dashes to create a valid Docker image name. If there
// is a version in the package name, the @ is replaced with a dash.
// For local paths, we clean up the path to make it a valid image name.
func packageNameToImageName(packageName string) string {
	imageName := packageName

	// Handle local paths by cleaning them up
	imageName = strings.TrimPrefix(imageName, "./")
	imageName = strings.TrimPrefix(imageName, "../")

	// Replace problematic characters
	imageName = strings.ReplaceAll(imageName, "/", "-")
	imageName = strings.ReplaceAll(imageName, "@", "-")
	imageName = strings.ReplaceAll(imageName, ".", "-")

	// Ensure the name doesn't start with a dash
	imageName = strings.TrimPrefix(imageName, "-")

	// If the name is empty after cleaning, use a default
	if imageName == "" || imageName == "-" {
		imageName = "toolhive-container"
	}

	return imageName
}

// isLocalGoPath checks if the given path is a local Go path that should be copied into the container.
// Local paths start with "." (relative) or "/" (absolute).
func isLocalGoPath(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") || path == "."
}

// IsImageProtocolScheme checks if the serverOrImage string contains a protocol scheme (uvx://, npx://, or go://)
func IsImageProtocolScheme(serverOrImage string) bool {
	return strings.HasPrefix(serverOrImage, UVXScheme) ||
		strings.HasPrefix(serverOrImage, NPXScheme) ||
		strings.HasPrefix(serverOrImage, GOScheme)
}

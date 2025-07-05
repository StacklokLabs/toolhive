package container

import (
	"fmt"
	"strings"
	"time"
)

// GetOrGenerateContainerName generates a container name if not provided.
// It returns both the container name and the base name.
// If containerName is not empty, it will be used as both the container name and base name.
// If containerName is empty, a name will be generated based on the image.
func GetOrGenerateContainerName(containerName, image string) (string, string) {
	var baseName string

	if containerName == "" {
		// Generate a container name from the image
		baseName = generateContainerBaseName(image)
		containerName = appendTimestamp(baseName)
	} else {
		// If container name is provided, use it as the base name
		baseName = containerName
	}

	return containerName, baseName
}

// generateContainerBaseName generates a base name for a container from the image name
func generateContainerBaseName(image string) string {
	// Extract the base name from the image, preserving registry namespaces
	// Examples:
	// - "nginx:latest" -> "nginx"
	// - "docker.io/library/nginx:latest" -> "docker.io-library-nginx"
	// - "quay.io/stacklok/mcp-server:v1" -> "quay.io-stacklok-mcp-server"

	// First, remove the tag part (everything after the colon)
	imageWithoutTag := strings.Split(image, ":")[0]

	// Replace slashes with dashes to preserve namespace structure
	namespaceName := strings.ReplaceAll(imageWithoutTag, "/", "-")

	// Sanitize the name (allow alphanumeric, dashes)
	var sanitizedName strings.Builder
	for _, c := range namespaceName {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			sanitizedName.WriteRune(c)
		} else {
			sanitizedName.WriteRune('-')
		}
	}

	return sanitizedName.String()
}

// appendTimestamp appends a timestamp to a base name to ensure uniqueness
func appendTimestamp(baseName string) string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("%s-%d", baseName, timestamp)
}

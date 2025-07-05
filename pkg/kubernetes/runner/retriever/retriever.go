// Package retriever contains logic for fetching or building MCP servers.
package retriever

import (
	"context"
	"errors"
	"fmt"

	nameref "github.com/google/go-containerregistry/pkg/name"

	"github.com/stacklok/toolhive/pkg/kubernetes/config"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/images"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/verifier"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/registry"
	"github.com/stacklok/toolhive/pkg/kubernetes/runner"
)

const (
	// VerifyImageWarn prints a warning when image validation fails.
	VerifyImageWarn = "warn"
	// VerifyImageEnabled treats validation failure as a fatal error.
	VerifyImageEnabled = "enabled"
	// VerifyImageDisabled turns off validation.
	VerifyImageDisabled = "disabled"
)

var (
	// ErrBadProtocolScheme is returned when the provided serverOrImage is not a valid protocol scheme.
	ErrBadProtocolScheme = errors.New("invalid protocol scheme provided for MCP server")
	// ErrImageNotFound is returned when the specified image is not found in the registry.
	ErrImageNotFound = errors.New("image not found in registry, please check the image name or tag")
)

// GetMCPServer retrieves the MCP server definition from the registry.
func GetMCPServer(
	ctx context.Context,
	serverOrImage string,
	rawCACertPath string,
	verificationType string,
) (string, *registry.ImageMetadata, error) {
	var imageMetadata *registry.ImageMetadata
	var imageToUse string

	imageManager := images.NewImageManager(ctx)
	// Check if the serverOrImage is a protocol scheme, e.g., uvx://, npx://, or go://
	if runner.IsImageProtocolScheme(serverOrImage) {
		logger.Debugf("Detected protocol scheme: %s", serverOrImage)
		// Process the protocol scheme and build the image
		caCertPath := resolveCACertPath(rawCACertPath)
		generatedImage, err := runner.HandleProtocolScheme(ctx, imageManager, serverOrImage, caCertPath)
		if err != nil {
			return "", nil, errors.Join(ErrBadProtocolScheme, err)
		}
		// Update the image in the runConfig with the generated image
		logger.Debugf("Using built image: %s instead of %s", generatedImage, serverOrImage)
		imageToUse = generatedImage
	} else {
		logger.Debugf("No protocol scheme detected, using image: %s", serverOrImage)
		// Try to find the imageMetadata in the registry
		provider, err := registry.GetDefaultProvider()
		if err != nil {
			return "", nil, fmt.Errorf("failed to get registry provider: %v", err)
		}
		imageMetadata, err = provider.GetServer(serverOrImage)
		if err != nil {
			logger.Debugf("ImageMetadata '%s' not found in registry: %v", serverOrImage, err)
			imageToUse = serverOrImage
		} else {
			logger.Debugf("Found imageMetadata '%s' in registry: %v", serverOrImage, imageMetadata)
			imageToUse = imageMetadata.Image
		}
	}

	// Verify the image against the expected provenance info (if applicable)
	if err := verifyImage(imageToUse, imageMetadata, verificationType); err != nil {
		return "", nil, err
	}

	// Pull the image if necessary
	if err := pullImage(ctx, imageToUse, imageManager); err != nil {
		return "", nil, fmt.Errorf("failed to retrieve or pull image: %v", err)
	}

	return imageToUse, imageMetadata, nil
}

// pullImage pulls an image from a remote registry if it has the "latest" tag
// or if it doesn't exist locally. If the image is a local image, it will not be pulled.
// If the image has the latest tag, it will be pulled to ensure we have the most recent version.
// however, if there is a failure in pulling the "latest" tag, it will check if the image exists locally
// as it is possible that the image was locally built.
func pullImage(ctx context.Context, image string, imageManager images.ImageManager) error {
	// Check if the image has the "latest" tag
	isLatestTag := hasLatestTag(image)

	if isLatestTag {
		// For "latest" tag, try to pull first
		logger.Infof("Image %s has 'latest' tag, pulling to ensure we have the most recent version...", image)
		err := imageManager.PullImage(ctx, image)
		if err != nil {
			// Pull failed, check if it exists locally
			logger.Infof("Pull failed, checking if image exists locally: %s", image)
			imageExists, checkErr := imageManager.ImageExists(ctx, image)
			if checkErr != nil {
				return fmt.Errorf("failed to check if image exists: %v", checkErr)
			}

			if imageExists {
				logger.Debugf("Using existing local image: %s", image)
			} else {
				return fmt.Errorf("%w: %s", ErrImageNotFound, image)
			}
		} else {
			logger.Infof("Successfully pulled image: %s", image)
		}
	} else {
		// For non-latest tags, check locally first
		logger.Debugf("Checking if image exists locally: %s", image)
		imageExists, err := imageManager.ImageExists(ctx, image)
		logger.Debugf("ImageExists locally: %t", imageExists)
		if err != nil {
			return fmt.Errorf("failed to check if image exists locally: %v", err)
		}

		if imageExists {
			logger.Debugf("Using existing local image: %s", image)
		} else {
			// Image doesn't exist locally, try to pull
			logger.Infof("Image %s not found locally, pulling...", image)
			if err := imageManager.PullImage(ctx, image); err != nil {
				// TODO: need more fine grained error handling here.
				return fmt.Errorf("%w: %s", ErrImageNotFound, image)
			}
			logger.Infof("Successfully pulled image: %s", image)
		}
	}

	return nil
}

// resolveCACertPath determines the CA certificate path to use, prioritizing command-line flag over configuration
func resolveCACertPath(flagValue string) string {
	// If command-line flag is provided, use it (highest priority)
	if flagValue != "" {
		return flagValue
	}

	// Otherwise, check configuration
	cfg := config.GetConfig()
	if cfg.CACertificatePath != "" {
		logger.Debugf("Using configured CA certificate: %s", cfg.CACertificatePath)
		return cfg.CACertificatePath
	}

	// No CA certificate configured
	return ""
}

// verifyImage verifies the image using the specified verification setting (warn, enabled, or disabled)
func verifyImage(image string, server *registry.ImageMetadata, verifySetting string) error {
	switch verifySetting {
	case VerifyImageDisabled:
		logger.Warn("Image verification is disabled")
	case VerifyImageWarn, VerifyImageEnabled:
		// Create a new verifier
		v, err := verifier.New(server)
		if err != nil {
			// This happens if we have no provenance entry in the registry for this server.
			// Not finding provenance info in the registry is not a fatal error if the setting is "warn".
			if errors.Is(err, verifier.ErrProvenanceServerInformationNotSet) && verifySetting == VerifyImageWarn {
				logger.Warnf("MCP server %s has no provenance information set, skipping image verification", image)
				return nil
			}
			return err
		}

		// Verify the image passing the server info
		isSafe, err := v.VerifyServer(image, server)
		if err != nil {
			return fmt.Errorf("image verification failed: %v", err)
		}
		if !isSafe {
			if verifySetting == VerifyImageWarn {
				logger.Warnf("MCP server %s failed image verification", image)
			} else {
				return fmt.Errorf("MCP server %s failed image verification", image)
			}
		} else {
			logger.Infof("MCP server %s is verified successfully", image)
		}
	default:
		return fmt.Errorf("invalid value for --image-verification: %s", verifySetting)
	}
	return nil
}

// hasLatestTag checks if the given image reference has the "latest" tag or no tag (which defaults to "latest")
func hasLatestTag(imageRef string) bool {
	ref, err := nameref.ParseReference(imageRef)
	if err != nil {
		// If we can't parse the reference, assume it's not "latest"
		logger.Warnf("Warning: Failed to parse image reference: %v", err)
		return false
	}

	// Check if the reference is a tag
	if taggedRef, ok := ref.(nameref.Tag); ok {
		// Check if the tag is "latest"
		return taggedRef.TagStr() == "latest"
	}

	// If the reference is not a tag (e.g., it's a digest), it's not "latest"
	// If no tag was specified, it defaults to "latest"
	_, isDigest := ref.(nameref.Digest)
	return !isDigest
}

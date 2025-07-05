package verifier

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	containerdigest "github.com/opencontainers/go-digest"
	"github.com/sigstore/sigstore-go/pkg/bundle"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// bundleFromAttestation retrieves the attestation bundles from the image reference. Note that the attestation
// bundles are stored as OCI image references. The function uses the referrers API to get the attestation. GitHub supports
// discovering the attestations via their API, but this is not supported here for now.
func bundleFromAttestation(imageRef string, auth authn.Authenticator, remoteOpts []remote.Option) ([]sigstoreBundle, error) {
	var bundles []sigstoreBundle

	// Get the auth options
	opts := []remote.Option{remote.WithAuth(auth)}

	// Get the image reference
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("error parsing image reference: %w", err)
	}

	// Get the image descriptor
	desc, err := remote.Get(ref, opts...)
	if err != nil {
		return nil, fmt.Errorf("error getting image descriptor: %w", err)
	}

	// Get the digest
	digest := ref.Context().Digest(desc.Digest.String())

	// Get the digest in bytes
	digestByte, err := hex.DecodeString(desc.Digest.Hex)
	if err != nil {
		return nil, err
	}

	// Use the referrers API to get the attestation reference
	referrers, err := remote.Referrers(digest, remoteOpts...)
	if err != nil {
		return nil, fmt.Errorf("error getting referrers: %w, %s", ErrProvenanceNotFoundOrIncomplete, err.Error())
	}

	refManifest, err := referrers.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("error getting referrers manifest: %w, %s", ErrProvenanceNotFoundOrIncomplete, err.Error())
	}

	// Loop through all available attestations and extract the bundle
	for _, refDesc := range refManifest.Manifests {
		if !strings.HasPrefix(refDesc.ArtifactType, "application/vnd.dev.sigstore.bundle") {
			continue
		}
		refImg, err := remote.Image(ref.Context().Digest(refDesc.Digest.String()), remoteOpts...)
		if err != nil {
			logger.Debugf("error getting referrer image: %w", err)
			continue
		}
		layers, err := refImg.Layers()
		if err != nil {
			logger.Debugf("error getting referrer image: %w", err)
			continue
		}
		layer0, err := layers[0].Uncompressed()
		if err != nil {
			logger.Debugf("error getting referrer image: %w", err)
			continue
		}
		bundleBytes, err := io.ReadAll(layer0)
		if err != nil {
			logger.Debugf("error getting referrer image: %w", err)
			continue
		}
		b := &bundle.Bundle{}
		err = b.UnmarshalJSON(bundleBytes)
		if err != nil {
			logger.Debugf("error unmarshalling bundle: %w", err)
			continue
		}

		bundles = append(bundles, sigstoreBundle{
			bundle:      b,
			digestBytes: digestByte,
			digestAlgo:  containerdigest.Canonical.String(),
		})
	}
	if len(bundles) == 0 {
		return nil, ErrProvenanceNotFoundOrIncomplete
	}
	return bundles, nil
}

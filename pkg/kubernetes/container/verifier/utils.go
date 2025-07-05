package verifier

import (
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

//go:embed tufroots
var embeddedTufRoots embed.FS

var (
	// ErrProvenanceNotFoundOrIncomplete is returned when there's no provenance info (missing .sig or attestation) or
	// has incomplete data
	ErrProvenanceNotFoundOrIncomplete = errors.New("provenance not found or incomplete")

	// ErrProvenanceServerInformationNotSet is returned when the provenance information for a server is not set
	ErrProvenanceServerInformationNotSet = errors.New("provenance server information not set")

	// MaxAttestationsBytesLimit is the maximum number of bytes we're willing to read from the attestation endpoint
	// We'll limit this to 10mb for now
	MaxAttestationsBytesLimit int64 = 10 * 1024 * 1024
)

const (
	sigstoreBundleMediaType01 = "application/vnd.dev.sigstore.bundle+json;version=0.1"
	// githubTokenIssuer is the issuer stamped into sigstore certs
	// when authenticating through GitHub tokens
	//nolint: gosec // Not an embedded credential
	githubTokenIssuer = "https://token.actions.githubusercontent.com"
)

func verifierOptions(trustedRoot string) ([]verify.VerifierOption, error) {
	switch trustedRoot {
	case TrustedRootSigstorePublicGoodInstance:
		return []verify.VerifierOption{
			verify.WithSignedCertificateTimestamps(1),
			verify.WithTransparencyLog(1),
			verify.WithObserverTimestamps(1),
		}, nil
	case TrustedRootSigstoreGitHub:
		return []verify.VerifierOption{
			verify.WithObserverTimestamps(1),
		}, nil
	}
	return nil, fmt.Errorf("unknown trusted root: %s", trustedRoot)
}

func getSigstoreOptions(sigstoreTUFRepoURL string) (*tuf.Options, []verify.VerifierOption, error) {
	// Default the sigstoreTUFRepoURL to the sigstore public trusted root repo if not provided
	if sigstoreTUFRepoURL == "" {
		sigstoreTUFRepoURL = TrustedRootSigstorePublicGoodInstance
	}

	// Get the Sigstore TUF client options
	tufOpts, err := getTUFOptions(sigstoreTUFRepoURL)
	if err != nil {
		return nil, nil, err
	}

	// Get the Sigstore verifier options
	opts, err := verifierOptions(sigstoreTUFRepoURL)
	if err != nil {
		return nil, nil, err
	}

	// All good
	return tufOpts, opts, nil
}

func getTUFOptions(sigstoreTUFRepoURL string) (*tuf.Options, error) {
	// Default the TUF options
	tufOpts := tuf.DefaultOptions()
	tufOpts.DisableLocalCache = true

	// Set the repository base URL, fix the scheme if not provided
	tufURL, err := url.Parse(sigstoreTUFRepoURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing sigstore TUF repo URL: %w", err)
	}
	if tufURL.Scheme == "" {
		tufURL.Scheme = "https"
	}
	tufOpts.RepositoryBaseURL = tufURL.String()

	// sigstore-go has a copy of the root.json for the public sigstore instance embedded. Nothing to do.
	if sigstoreTUFRepoURL != TrustedRootSigstorePublicGoodInstance {
		// Look up and set the embedded root.json for the given TUF repository
		rootJson, err := embeddedRootJson(sigstoreTUFRepoURL)
		if err != nil {
			return nil, fmt.Errorf("error getting embedded root.json for %s: %w", sigstoreTUFRepoURL, err)
		}
		tufOpts.Root = rootJson
	}

	// All good
	return tufOpts, nil
}

func embeddedRootJson(tufRootURL string) ([]byte, error) {
	embeddedRootPath := path.Join("tufroots", tufRootURL, rootTUFPath)
	return embeddedTufRoots.ReadFile(embeddedRootPath)
}

// getSigstoreBundles returns the sigstore bundles, either through the OCI registry or the GitHub attestation endpoint
func getSigstoreBundles(
	imageRef string,
) ([]sigstoreBundle, error) {
	auth := authn.Anonymous
	// Try to build a bundle from a Sigstore signed image
	bundles, err := bundleFromSigstoreSignedImage(imageRef, auth)
	if errors.Is(err, ErrProvenanceNotFoundOrIncomplete) {
		// If we get this error, it means that the image is not signed
		// or the signature is incomplete. Let's try to see if we can find attestation for the image.
		return bundleFromAttestation(imageRef, auth, []remote.Option{})
	} else if err != nil {
		return nil, err
	}
	// If we get here, it means that we got a bundle from a Sigstore signed image
	return bundles, nil
}

package verifier

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/registry"
)

const (
	// TrustedRootSigstoreGitHub is the GitHub trusted root repository for sigstore (used for private repos, Enterprise)
	TrustedRootSigstoreGitHub = "tuf-repo.github.com"
	// TrustedRootSigstorePublicGoodInstance is the public trusted root repository for sigstore
	TrustedRootSigstorePublicGoodInstance = "tuf-repo-cdn.sigstore.dev"
	// RootTUFPath is the path to the root.json file inside an embedded TUF repository
	rootTUFPath = "root.json"
)

// Sigstore is the sigstore verifier
type Sigstore struct {
	verifier *verify.Verifier
}

// Result is the result of the verification
type Result struct {
	IsSigned   bool `json:"is_signed"`
	IsVerified bool `json:"is_verified"`
	verify.VerificationResult
}

// New creates a new Sigstore verifier
func New(serverInfo *registry.ImageMetadata) (*Sigstore, error) {
	// Fail the verification early if the server information is not set
	if serverInfo == nil || serverInfo.Provenance == nil {
		return nil, ErrProvenanceServerInformationNotSet
	}
	sigstoreTUFRepoURL := serverInfo.Provenance.SigstoreURL

	// Default the sigstoreTUFRepoURL to the sigstore public trusted root repo if not provided.
	// Note: Update this if we want to support more sigstore instances
	if sigstoreTUFRepoURL == "" {
		sigstoreTUFRepoURL = TrustedRootSigstorePublicGoodInstance
	}

	// Get the sigstore options for the TUF client and the verifier
	tufOpts, opts, err := getSigstoreOptions(sigstoreTUFRepoURL)
	if err != nil {
		return nil, err
	}

	// Get the trusted material - sigstore's trusted_root.json
	trustedMaterial, err := root.FetchTrustedRootWithOptions(tufOpts)
	if err != nil {
		return nil, err
	}

	sev, err := verify.NewVerifier(trustedMaterial, opts...)
	if err != nil {
		return nil, err
	}

	// return the verifier
	return &Sigstore{
		verifier: sev,
	}, nil
}

// GetVerificationResults returns the verification results for the given image reference
func (s *Sigstore) GetVerificationResults(
	imageRef string,
) ([]*verify.VerificationResult, error) {
	// Construct the bundle(s) for the image reference
	bundles, err := getSigstoreBundles(imageRef)
	if err != nil && !errors.Is(err, ErrProvenanceNotFoundOrIncomplete) {
		// We got some other unexpected error prior to querying for the signature/attestation
		return nil, err
	}
	logger.Debugf("Number of sigstore bundles we managed to construct is %d", len(bundles))

	// If we didn't manage to construct any valid bundles, it probably means that the image is not signed.
	if len(bundles) == 0 || errors.Is(err, ErrProvenanceNotFoundOrIncomplete) {
		return []*verify.VerificationResult{}, nil
	}

	// Construct the verification result for each bundle we managed to generate.
	return getVerifiedResults(s.verifier, bundles), nil
}

// getVerifiedResults verifies the artifact using the bundles against the configured sigstore instance
// and returns the extracted metadata that we need for ingestion
func getVerifiedResults(
	sev *verify.Verifier,
	bundles []sigstoreBundle,
) []*verify.VerificationResult {
	var results []*verify.VerificationResult

	// Verify each bundle we've constructed
	for _, b := range bundles {
		// Create a new verification result. At this point, we managed to extract a bundle, so lets verify it.
		verificationResult, err := sev.Verify(b.bundle, verify.NewPolicy(
			verify.WithArtifactDigest(b.digestAlgo, b.digestBytes),
			verify.WithoutIdentitiesUnsafe(),
		))
		if err != nil {
			logger.Info("bundle verification failed: %v", err)
			continue
		}
		// We've successfully verified and extracted the artifact provenance information
		results = append(results, verificationResult)
	}
	// Return the results
	return results
}

// VerifyServer verifies the server information for the given image reference
func (s *Sigstore) VerifyServer(imageRef string, serverInfo *registry.ImageMetadata) (bool, error) {
	// Get the verification results for the image reference
	results, err := s.GetVerificationResults(imageRef)
	if err != nil {
		return false, err
	}

	// If we didn't manage to get any verification results, it probably means that the image is not signed.
	if len(results) == 0 {
		return false, nil
	}

	// Compare the server information with the verification results
	for _, res := range results {
		if !isVerificationResultMatchingServerProvenance(res, serverInfo.Provenance) {
			// The server information does not match the verification result, fail the verification
			return false, nil
		}
	}
	// The server information matches the verification result, pass the verification
	return true, nil
}

func isVerificationResultMatchingServerProvenance(r *verify.VerificationResult, p *registry.Provenance) bool {
	if r == nil || p == nil || r.Signature == nil || r.Signature.Certificate == nil {
		return false
	}

	// Compare the base properties of the verification result and the server provenance
	if !compareBaseProperties(r, p) {
		return false
	}

	// If the attestations are not set, we can skip this check
	if p.Attestation != nil && r.Statement != nil && p.Attestation.Predicate != nil && r.Statement.Predicate != nil {
		if p.Attestation.PredicateType != r.Statement.PredicateType {
			return false
		}
		return reflect.DeepEqual(p.Attestation.Predicate, r.Statement.Predicate)
	}

	return true
}

// compareBaseProperties compares the base properties of the verification result and the server provenance
func compareBaseProperties(r *verify.VerificationResult, p *registry.Provenance) bool {
	// Extract the signer identity from the certificate
	siIdentity, err := signerIdentityFromCertificate(r.Signature.Certificate)
	if err != nil {
		logger.Error("error parsing signer identity")
	}
	// Compare repository name and reference, signer identity, runner environment, and cert issuer
	if p.RepositoryURI != "" {
		// If the repository URI is set, we need to compare it with the verification result
		if p.RepositoryURI != r.Signature.Certificate.SourceRepositoryURI {
			return false
		}
	}
	if p.RepositoryRef != "" {
		// If the repository reference is set, we need to compare it with the verification result
		if p.RepositoryRef != r.Signature.Certificate.SourceRepositoryRef {
			return false
		}
	}
	if p.RunnerEnvironment != "" {
		// If the runner environment is set, we need to compare it with the verification result
		if p.RunnerEnvironment != r.Signature.Certificate.RunnerEnvironment {
			return false
		}
	}
	if p.CertIssuer != "" {
		// If the certificate issuer is set, we need to compare it with the verification result
		if p.CertIssuer != r.Signature.Certificate.Issuer {
			return false
		}
	}
	if p.SignerIdentity != "" {
		// If the signer identity is set, we need to compare it with the verification result
		if p.SignerIdentity != siIdentity {
			return false
		}
	}
	return true
}

// signerIdentityFromCertificate returns the signer identity. When the identity
// is a URI (from the BuildSignerURI extension or the cert SAN), we return only
// the URI path component. We split it this way to ensure we can make rules
// more generalizable (applicable to the same path regardless of the repo for example).
func signerIdentityFromCertificate(c *certificate.Summary) (string, error) {
	var builderURL string

	if c.SubjectAlternativeName == "" {
		return "", fmt.Errorf("certificate has no signer identity in SAN (is it a fulcio cert?)")
	}

	switch {
	case c.SubjectAlternativeName != "":
		builderURL = c.SubjectAlternativeName
	default:
		// Return the SAN in the cert as a last resort. This handles the case when
		// we don't have a signer identity but also when the SAN is an email
		// when a user authenticated using an OIDC provider or a SPIFFE ID.
		// Any other SAN types are returned verbatim
		return c.SubjectAlternativeName, nil
	}

	// Any signer identity not issued by github actions is returned verbatim
	if c.Issuer != githubTokenIssuer {
		return builderURL, nil
	}

	// When handling a cert issued through GitHub actions tokens, break the identity
	// into its components. The verifier captures the git reference and the
	// the repository URI.
	if c.SourceRepositoryURI == "" {
		return "", fmt.Errorf(
			"certificate extension dont have a SourceRepositoryURI set (oid 1.3.6.1.4.1.57264.1.5)",
		)
	}

	builderURL, _, _ = strings.Cut(builderURL, "@")
	builderURL = strings.TrimPrefix(builderURL, c.SourceRepositoryURI)

	return builderURL, nil
}

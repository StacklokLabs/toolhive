// Package verifier provides a client for verifying artifacts using sigstore
package verifier

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

type sigstoreBundle struct {
	bundle      *bundle.Bundle
	digestBytes []byte
	digestAlgo  string
}

// bundleFromSigstoreSignedImage returns a bundle from a Sigstore signed image
func bundleFromSigstoreSignedImage(imageRef string, auth authn.Authenticator) ([]sigstoreBundle, error) {
	// Get the signature manifest from the OCI image reference
	signatureRef, err := getSignatureReferenceFromOCIImage(imageRef, auth)
	if err != nil {
		return nil, fmt.Errorf("error getting signature reference from OCI image: %w", err)
	}

	// Parse the manifest and return a list of all simple signing layers we managed to extract
	simpleSigningLayers, err := getSimpleSigningLayersFromSignatureManifest(signatureRef, auth)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrProvenanceNotFoundOrIncomplete, err.Error())
	}

	// Loop through each and build the sigstore bundles
	var bundles []sigstoreBundle
	for _, layer := range simpleSigningLayers {
		// Build the verification material for the bundle
		verificationMaterial, err := getBundleVerificationMaterial(layer)
		if err != nil {
			logger.Error("error getting bundle verification material")
			continue
		}

		// Build the message signature for the bundle
		msgSignature, err := getBundleMsgSignature(layer)
		if err != nil {
			logger.Error("error getting bundle message signature")
			continue
		}

		// Construct and verify the bundle
		pbb := protobundle.Bundle{
			MediaType:            sigstoreBundleMediaType01,
			VerificationMaterial: verificationMaterial,
			Content:              msgSignature,
		}
		bun, err := bundle.NewBundle(&pbb)
		if err != nil {
			logger.Error("error creating protobuf bundle")
			continue
		}

		// Collect the digest of the simple signing layer (this is what is signed)
		digestBytes, err := hex.DecodeString(layer.Digest.Hex)
		if err != nil {
			logger.Error("error decoding the simplesigning layer digest")
			continue
		}

		// Store the bundle and the certificate identity we extracted from the simple signing layer
		bundles = append(bundles, sigstoreBundle{
			bundle:      bun,
			digestAlgo:  layer.Digest.Algorithm,
			digestBytes: digestBytes,
		})
	}

	// There's no available provenance information about this image if we failed to find valid bundles from the list
	// of simple signing layers
	if len(bundles) == 0 {
		return nil, ErrProvenanceNotFoundOrIncomplete
	}

	// Return the bundles
	return bundles, nil
}

// getSignatureReferenceFromOCIImage returns the simple signing layer from the OCI image reference
func getSignatureReferenceFromOCIImage(imageRef string, auth authn.Authenticator) (string, error) {
	// 0. Get the auth options
	opts := []remote.Option{remote.WithAuth(auth)}

	// 1. Get the image reference
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", fmt.Errorf("error parsing image reference: %w", err)
	}

	// 2. Get the image descriptor
	desc, err := remote.Get(ref, opts...)
	if err != nil {
		return "", fmt.Errorf("error getting image descriptor: %w", err)
	}

	// 3. Get the digest
	digest := ref.Context().Digest(desc.Digest.String())
	h, err := v1.NewHash(digest.Identifier())
	if err != nil {
		return "", fmt.Errorf("error getting hash: %w", err)
	}

	// 4. Construct the signature reference - sha256-<hash>.sig
	sigTag := digest.Context().Tag(fmt.Sprint(h.Algorithm, "-", h.Hex, ".sig"))

	// 5. Return the reference
	return sigTag.Name(), nil
}

// getSimpleSigningLayersFromSignatureManifest returns the identity and issuer from the certificate
func getSimpleSigningLayersFromSignatureManifest(manifestRef string, auth authn.Authenticator) ([]v1.Descriptor, error) {
	craneOpts := []crane.Option{crane.WithAuth(auth)}
	// Get the manifest of the signature
	mf, err := crane.Manifest(manifestRef, craneOpts...)
	if err != nil {
		return nil, fmt.Errorf("error getting signature manifest: %w", err)
	}

	// Parse the manifest
	r := io.LimitReader(bytes.NewReader(mf), MaxAttestationsBytesLimit)
	manifest, err := v1.ParseManifest(r)
	if err != nil {
		return nil, fmt.Errorf("error parsing signature manifest: %w", err)
	}

	// Loop through its layers and extract the simple signing layers
	var results []v1.Descriptor
	for _, layer := range manifest.Layers {
		if layer.MediaType == "application/vnd.dev.cosign.simplesigning.v1+json" {
			// We found a simple signing layer, store and return it even if we may fail to parse it later
			results = append(results, layer)
		}
	}

	// Return the results - we may not have found any simple signing layers, but we still return the results
	return results, nil
}

// getBundleVerificationMaterial returns the bundle verification material from the simple signing layer
func getBundleVerificationMaterial(manifestLayer v1.Descriptor) (
	*protobundle.VerificationMaterial, error) {
	// 1. Get the signing certificate chain
	signingCert, err := getVerificationMaterialX509CertificateChain(manifestLayer)
	if err != nil {
		return nil, fmt.Errorf("error getting signing certificate: %w", err)
	}

	// 2. Get the transparency log entries
	tlogEntries, err := getVerificationMaterialTlogEntries(manifestLayer)
	if err != nil {
		return nil, fmt.Errorf("error getting tlog entries: %w", err)
	}
	// 3. Construct the verification material
	return &protobundle.VerificationMaterial{
		Content:                   signingCert,
		TlogEntries:               tlogEntries,
		TimestampVerificationData: nil,
	}, nil
}

// getVerificationMaterialX509CertificateChain returns the verification material X509 certificate chain from the
// simple signing layer
func getVerificationMaterialX509CertificateChain(manifestLayer v1.Descriptor) (
	*protobundle.VerificationMaterial_X509CertificateChain, error) {
	// 1. Get the PEM certificate from the simple signing layer
	pemCert := manifestLayer.Annotations["dev.sigstore.cosign/certificate"]
	// 2. Construct the DER encoded version of the PEM certificate
	block, _ := pem.Decode([]byte(pemCert))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}
	signingCert := protocommon.X509Certificate{
		RawBytes: block.Bytes,
	}
	// 3. Construct the X509 certificate chain
	return &protobundle.VerificationMaterial_X509CertificateChain{
		X509CertificateChain: &protocommon.X509CertificateChain{
			Certificates: []*protocommon.X509Certificate{&signingCert},
		},
	}, nil
}

// getVerificationMaterialTlogEntries returns the verification material transparency log entries from the simple signing layer
func getVerificationMaterialTlogEntries(manifestLayer v1.Descriptor) (
	[]*protorekor.TransparencyLogEntry, error) {
	// 1. Get the bundle annotation
	bun := manifestLayer.Annotations["dev.sigstore.cosign/bundle"]
	var jsonData map[string]interface{}
	err := json.Unmarshal([]byte(bun), &jsonData)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling json: %w", err)
	}
	// 2. Get the log index, log ID, integrated time, signed entry timestamp and body
	logIndex, ok := jsonData["Payload"].(map[string]interface{})["logIndex"].(float64)
	if !ok {
		return nil, fmt.Errorf("error getting logIndex")
	}
	logIndexInt64 := int64(logIndex)
	li, ok := jsonData["Payload"].(map[string]interface{})["logID"].(string)
	if !ok {
		return nil, fmt.Errorf("error getting logID")
	}
	logID, err := hex.DecodeString(li)
	if err != nil {
		return nil, fmt.Errorf("error decoding logID: %w", err)
	}
	integratedTime, ok := jsonData["Payload"].(map[string]interface{})["integratedTime"].(float64)
	if !ok {
		return nil, fmt.Errorf("error getting integratedTime")
	}
	set, ok := jsonData["SignedEntryTimestamp"].(string)
	if !ok {
		return nil, fmt.Errorf("error getting SignedEntryTimestamp")
	}
	signedEntryTimestamp, err := base64.StdEncoding.DecodeString(set)
	if err != nil {
		return nil, fmt.Errorf("error decoding signedEntryTimestamp: %w", err)
	}
	// 3. Unmarshal the body and extract the rekor KindVersion details
	body, ok := jsonData["Payload"].(map[string]interface{})["body"].(string)
	if !ok {
		return nil, fmt.Errorf("error getting body")
	}
	bodyBytes, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("error decoding body: %w", err)
	}
	err = json.Unmarshal(bodyBytes, &jsonData)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling json: %w", err)
	}
	apiVersion := jsonData["apiVersion"].(string)
	kind := jsonData["kind"].(string)
	// 4. Construct the transparency log entry list
	return []*protorekor.TransparencyLogEntry{
		{
			LogIndex: logIndexInt64,
			LogId: &protocommon.LogId{
				KeyId: logID,
			},
			KindVersion: &protorekor.KindVersion{
				Kind:    kind,
				Version: apiVersion,
			},
			IntegratedTime: int64(integratedTime),
			InclusionPromise: &protorekor.InclusionPromise{
				SignedEntryTimestamp: signedEntryTimestamp,
			},
			InclusionProof:    nil,
			CanonicalizedBody: bodyBytes,
		},
	}, nil
}

// getBundleMsgSignature returns the bundle message signature from the simple signing layer
func getBundleMsgSignature(simpleSigningLayer v1.Descriptor) (*protobundle.Bundle_MessageSignature, error) {
	// 1. Get the message digest algorithm
	var msgHashAlg protocommon.HashAlgorithm
	switch simpleSigningLayer.Digest.Algorithm {
	case "sha256":
		msgHashAlg = protocommon.HashAlgorithm_SHA2_256
	default:
		return nil, fmt.Errorf("unknown digest algorithm: %s", simpleSigningLayer.Digest.Algorithm)
	}
	// 2. Get the message digest
	digest, err := hex.DecodeString(simpleSigningLayer.Digest.Hex)
	if err != nil {
		return nil, fmt.Errorf("error decoding digest: %w", err)
	}
	// 3. Get the signature
	s := simpleSigningLayer.Annotations["dev.cosignproject.cosign/signature"]
	sig, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("error decoding manSig: %w", err)
	}
	// Construct the bundle message signature
	return &protobundle.Bundle_MessageSignature{
		MessageSignature: &protocommon.MessageSignature{
			MessageDigest: &protocommon.HashOutput{
				Algorithm: msgHashAlg,
				Digest:    digest,
			},
			Signature: sig,
		},
	}, nil
}

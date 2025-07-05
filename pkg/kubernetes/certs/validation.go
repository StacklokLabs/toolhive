// Package certs provides utilities for certificate validation and handling.
package certs

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// ValidateCACertificate validates that the provided data contains a valid PEM-encoded certificate
func ValidateCACertificate(certData []byte) error {
	// Check if the data contains PEM blocks
	block, _ := pem.Decode(certData)
	if block == nil {
		return fmt.Errorf("no PEM data found in certificate file")
	}

	// Check if it's a certificate block
	if block.Type != "CERTIFICATE" {
		return fmt.Errorf("PEM block is not a certificate (found: %s)", block.Type)
	}

	// Parse the certificate to ensure it's valid
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Basic validation - check if it's a CA certificate
	if !cert.IsCA {
		// Log a warning but don't fail - some corporate proxies use non-CA certificates
		logger.Warnf("Certificate is not marked as a CA certificate, but proceeding anyway")
	}

	return nil
}

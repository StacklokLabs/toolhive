// Package oauth provides OAuth 2.0 and OIDC authentication functionality.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OIDCDiscoveryDocument represents the OIDC discovery document structure
// This is a simplified wrapper around the Zitadel OIDC discovery
type OIDCDiscoveryDocument struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	UserinfoEndpoint              string   `json:"userinfo_endpoint"`
	JWKSURI                       string   `json:"jwks_uri"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// httpClient interface for dependency injection (private for testing)
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// DiscoverOIDCEndpoints discovers OAuth endpoints from an OIDC issuer
func DiscoverOIDCEndpoints(ctx context.Context, issuer string) (*OIDCDiscoveryDocument, error) {
	return discoverOIDCEndpointsWithClient(ctx, issuer, nil)
}

// discoverOIDCEndpointsWithClient discovers OAuth endpoints from an OIDC issuer with a custom HTTP client (private for testing)
func discoverOIDCEndpointsWithClient(ctx context.Context, issuer string, client httpClient) (*OIDCDiscoveryDocument, error) {
	// Validate issuer URL
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}

	// Ensure HTTPS for security (except localhost for development)
	if issuerURL.Scheme != "https" && !isLocalhost(issuerURL.Host) {
		return nil, fmt.Errorf("issuer must use HTTPS: %s", issuer)
	}

	// Construct the well-known endpoint URL
	wellKnownURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"

	// Create HTTP request with timeout
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set User-Agent header
	req.Header.Set("User-Agent", "ToolHive/1.0")
	req.Header.Set("Accept", "application/json")

	// Use provided client or create default HTTP client with timeout and security settings
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
			},
		}
	}

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OIDC configuration: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery endpoint returned status %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return nil, fmt.Errorf("unexpected content type: %s", contentType)
	}

	// Limit response size to prevent DoS
	const maxResponseSize = 1024 * 1024 // 1MB
	limitedReader := io.LimitReader(resp.Body, maxResponseSize)

	// Parse the response
	var doc OIDCDiscoveryDocument
	decoder := json.NewDecoder(limitedReader)
	// Allow unknown fields for better compatibility with different OIDC providers
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC configuration: %w", err)
	}

	// Validate that we got the required fields
	if err := validateOIDCDocument(&doc, issuer); err != nil {
		return nil, fmt.Errorf("invalid OIDC configuration: %w", err)
	}

	return &doc, nil
}

// validateOIDCDocument validates the OIDC discovery document
func validateOIDCDocument(doc *OIDCDiscoveryDocument, expectedIssuer string) error {
	if doc.Issuer == "" {
		return fmt.Errorf("missing issuer")
	}

	if doc.Issuer != expectedIssuer {
		return fmt.Errorf("issuer mismatch: expected %s, got %s", expectedIssuer, doc.Issuer)
	}

	if doc.AuthorizationEndpoint == "" {
		return fmt.Errorf("missing authorization_endpoint")
	}

	if doc.TokenEndpoint == "" {
		return fmt.Errorf("missing token_endpoint")
	}

	if doc.JWKSURI == "" {
		return fmt.Errorf("missing jwks_uri")
	}

	// Validate URLs
	endpoints := map[string]string{
		"authorization_endpoint": doc.AuthorizationEndpoint,
		"token_endpoint":         doc.TokenEndpoint,
		"jwks_uri":               doc.JWKSURI,
	}

	if doc.UserinfoEndpoint != "" {
		endpoints["userinfo_endpoint"] = doc.UserinfoEndpoint
	}

	for name, endpoint := range endpoints {
		if err := validateEndpointURL(endpoint); err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
	}

	return nil
}

// validateEndpointURL validates that an endpoint URL is secure
func validateEndpointURL(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Ensure HTTPS for security (except localhost for development)
	if u.Scheme != "https" && !isLocalhost(u.Host) {
		return fmt.Errorf("endpoint must use HTTPS: %s", endpoint)
	}

	return nil
}

// isLocalhost checks if a host is localhost (for development)
func isLocalhost(host string) bool {
	return strings.HasPrefix(host, "localhost:") ||
		strings.HasPrefix(host, "127.0.0.1:") ||
		strings.HasPrefix(host, "[::1]:") ||
		host == "localhost" ||
		host == "127.0.0.1" ||
		host == "[::1]"
}

// CreateOAuthConfigFromOIDC creates an OAuth config from OIDC discovery
func CreateOAuthConfigFromOIDC(
	ctx context.Context,
	issuer, clientID, clientSecret string,
	scopes []string,
	usePKCE bool,
	callbackPort int,
) (*Config, error) {
	return createOAuthConfigFromOIDCWithClient(ctx, issuer, clientID, clientSecret, scopes, usePKCE, callbackPort, nil)
}

// createOAuthConfigFromOIDCWithClient creates an OAuth config from OIDC discovery with a custom HTTP client (private for testing)
func createOAuthConfigFromOIDCWithClient(
	ctx context.Context,
	issuer, clientID, clientSecret string,
	scopes []string,
	usePKCE bool,
	callbackPort int,
	client httpClient,
) (*Config, error) {
	// Discover OIDC endpoints
	doc, err := discoverOIDCEndpointsWithClient(ctx, issuer, client)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC endpoints: %w", err)
	}

	// Default scopes if none provided
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}

	// Enable PKCE by default if supported
	if !usePKCE && len(doc.CodeChallengeMethodsSupported) > 0 {
		for _, method := range doc.CodeChallengeMethodsSupported {
			if method == "S256" {
				usePKCE = true
				break
			}
		}
	}

	return &Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthURL:      doc.AuthorizationEndpoint,
		TokenURL:     doc.TokenEndpoint,
		Scopes:       scopes,
		UsePKCE:      usePKCE,
		CallbackPort: callbackPort,
	}, nil
}

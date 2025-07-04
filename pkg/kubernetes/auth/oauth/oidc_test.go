package oauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	httpsScheme = "https"
)

// testDiscoverOIDCEndpoints is a test version that skips TLS verification
func testDiscoverOIDCEndpoints(
	ctx context.Context,
	t *testing.T,
	issuer string,
) (*OIDCDiscoveryDocument, error) {
	t.Helper()

	// Validate issuer URL
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}

	// Ensure HTTPS for security (except localhost for development)
	if issuerURL.Scheme != httpsScheme && !isLocalhost(issuerURL.Host) {
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

	// Create HTTP client with timeout and security settings (skip TLS verification for tests)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // Skip TLS verification for tests
		},
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
	limitedReader := http.MaxBytesReader(nil, resp.Body, maxResponseSize)

	// Parse the response
	var doc OIDCDiscoveryDocument
	decoder := json.NewDecoder(limitedReader)
	decoder.DisallowUnknownFields() // Strict parsing
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC configuration: %w", err)
	}

	// Validate that we got the required fields
	if err := validateOIDCDocument(&doc, issuer); err != nil {
		return nil, fmt.Errorf("invalid OIDC configuration: %w", err)
	}

	return &doc, nil
}

func TestDiscoverOIDCEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		issuer         string
		serverResponse func() *httptest.Server
		expectError    bool
		errorMsg       string
		validate       func(t *testing.T, doc *OIDCDiscoveryDocument)
	}{
		{
			name:        "invalid issuer URL",
			issuer:      "not-a-url",
			expectError: true,
			errorMsg:    "issuer must use HTTPS",
		},
		{
			name:        "non-HTTPS issuer (security check)",
			issuer:      "http://example.com",
			expectError: true,
			errorMsg:    "issuer must use HTTPS",
		},
		{
			name:   "localhost HTTP allowed for development",
			issuer: "http://localhost:8080",
			serverResponse: func() *httptest.Server {
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/.well-known/openid-configuration" {
						t.Errorf("unexpected path: %s", r.URL.Path)
					}

					// Use the actual server URL but replace 127.0.0.1 with localhost
					issuerURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

					doc := OIDCDiscoveryDocument{
						Issuer:                        issuerURL,
						AuthorizationEndpoint:         issuerURL + "/auth",
						TokenEndpoint:                 issuerURL + "/token",
						JWKSURI:                       issuerURL + "/jwks",
						UserinfoEndpoint:              issuerURL + "/userinfo",
						CodeChallengeMethodsSupported: []string{"S256", "plain"},
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
				return server
			},
			expectError: false,
			validate: func(t *testing.T, doc *OIDCDiscoveryDocument) {
				t.Helper()
				assert.True(t, strings.HasPrefix(doc.Issuer, "http://localhost:"))
				assert.True(t, strings.HasSuffix(doc.AuthorizationEndpoint, "/auth"))
				assert.True(t, strings.HasSuffix(doc.TokenEndpoint, "/token"))
				assert.True(t, strings.HasSuffix(doc.JWKSURI, "/jwks"))
				assert.Contains(t, doc.CodeChallengeMethodsSupported, "S256")
			},
		},
		{
			name:   "valid HTTPS discovery",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				var server *httptest.Server
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/.well-known/openid-configuration", r.URL.Path)
					assert.Equal(t, "ToolHive/1.0", r.Header.Get("User-Agent"))
					assert.Equal(t, "application/json", r.Header.Get("Accept"))

					doc := OIDCDiscoveryDocument{
						Issuer:                        server.URL,
						AuthorizationEndpoint:         server.URL + "/auth",
						TokenEndpoint:                 server.URL + "/token",
						JWKSURI:                       server.URL + "/jwks",
						UserinfoEndpoint:              server.URL + "/userinfo",
						CodeChallengeMethodsSupported: []string{"S256"},
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
				return server
			},
			expectError: false,
			validate: func(t *testing.T, doc *OIDCDiscoveryDocument) {
				t.Helper()
				// The issuer should match the server URL
				assert.True(t, strings.HasPrefix(doc.Issuer, "https://127.0.0.1:"))
				assert.True(t, strings.HasSuffix(doc.AuthorizationEndpoint, "/auth"))
				assert.True(t, strings.HasSuffix(doc.TokenEndpoint, "/token"))
				assert.True(t, strings.HasSuffix(doc.JWKSURI, "/jwks"))
				assert.True(t, strings.HasSuffix(doc.UserinfoEndpoint, "/userinfo"))
			},
		},
		{
			name:   "server returns non-200 status",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: true,
			errorMsg:    "OIDC discovery endpoint returned status 404",
		},
		{
			name:   "server returns wrong content type",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte("<html>Not JSON</html>"))
				}))
			},
			expectError: true,
			errorMsg:    "unexpected content type",
		},
		{
			name:   "server returns invalid JSON",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte("invalid json"))
				}))
			},
			expectError: true,
			errorMsg:    "failed to decode OIDC configuration",
		},
		{
			name:   "missing required fields",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				var server *httptest.Server
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					doc := OIDCDiscoveryDocument{
						Issuer: server.URL,
						// Missing required fields
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
				return server
			},
			expectError: true,
			errorMsg:    "missing authorization_endpoint",
		},
		{
			name:   "issuer mismatch (security check)",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					doc := OIDCDiscoveryDocument{
						Issuer:                "https://malicious.com", // Different issuer
						AuthorizationEndpoint: "https://malicious.com/auth",
						TokenEndpoint:         "https://malicious.com/token",
						JWKSURI:               "https://malicious.com/jwks",
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
			},
			expectError: true,
			errorMsg:    "issuer mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			issuer := tt.issuer

			if tt.serverResponse != nil {
				server = tt.serverResponse()
				defer server.Close()

				// Replace the issuer with the test server URL for tests that need a server
				if strings.Contains(tt.name, "localhost HTTP") {
					// For localhost test, replace the port but keep localhost
					issuer = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
				} else if strings.Contains(tt.name, "valid HTTPS discovery") ||
					strings.Contains(tt.name, "server returns") ||
					strings.Contains(tt.name, "missing required fields") ||
					strings.Contains(tt.name, "issuer mismatch") {
					issuer = server.URL
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			doc, err := testDiscoverOIDCEndpoints(ctx, t, issuer)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, doc)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, doc)

			if tt.validate != nil {
				tt.validate(t, doc)
			}
		})
	}
}

func TestValidateOIDCDocument(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		doc            *OIDCDiscoveryDocument
		expectedIssuer string
		expectError    bool
		errorMsg       string
	}{
		{
			name: "missing issuer",
			doc: &OIDCDiscoveryDocument{
				AuthorizationEndpoint: "https://example.com/auth",
				TokenEndpoint:         "https://example.com/token",
				JWKSURI:               "https://example.com/jwks",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "missing issuer",
		},
		{
			name: "issuer mismatch",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "https://malicious.com",
				AuthorizationEndpoint: "https://example.com/auth",
				TokenEndpoint:         "https://example.com/token",
				JWKSURI:               "https://example.com/jwks",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "issuer mismatch",
		},
		{
			name: "missing authorization endpoint",
			doc: &OIDCDiscoveryDocument{
				Issuer:        "https://example.com",
				TokenEndpoint: "https://example.com/token",
				JWKSURI:       "https://example.com/jwks",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "missing authorization_endpoint",
		},
		{
			name: "missing token endpoint",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "https://example.com",
				AuthorizationEndpoint: "https://example.com/auth",
				JWKSURI:               "https://example.com/jwks",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "missing token_endpoint",
		},
		{
			name: "missing JWKS URI",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "https://example.com",
				AuthorizationEndpoint: "https://example.com/auth",
				TokenEndpoint:         "https://example.com/token",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "missing jwks_uri",
		},
		{
			name: "invalid authorization endpoint URL",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "https://example.com",
				AuthorizationEndpoint: "not-a-url",
				TokenEndpoint:         "https://example.com/token",
				JWKSURI:               "https://example.com/jwks",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "invalid authorization_endpoint",
		},
		{
			name: "non-HTTPS endpoint (security check)",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "https://example.com",
				AuthorizationEndpoint: "http://example.com/auth",
				TokenEndpoint:         "https://example.com/token",
				JWKSURI:               "https://example.com/jwks",
			},
			expectedIssuer: "https://example.com",
			expectError:    true,
			errorMsg:       "invalid authorization_endpoint",
		},
		{
			name: "valid document",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "https://example.com",
				AuthorizationEndpoint: "https://example.com/auth",
				TokenEndpoint:         "https://example.com/token",
				JWKSURI:               "https://example.com/jwks",
				UserinfoEndpoint:      "https://example.com/userinfo",
			},
			expectedIssuer: "https://example.com",
			expectError:    false,
		},
		{
			name: "localhost endpoints allowed",
			doc: &OIDCDiscoveryDocument{
				Issuer:                "http://localhost:8080",
				AuthorizationEndpoint: "http://localhost:8080/auth",
				TokenEndpoint:         "http://localhost:8080/token",
				JWKSURI:               "http://localhost:8080/jwks",
			},
			expectedIssuer: "http://localhost:8080",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateOIDCDocument(tt.doc, tt.expectedIssuer)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEndpointURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		endpoint    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "invalid URL",
			endpoint:    "not-a-url",
			expectError: true,
			errorMsg:    "endpoint must use HTTPS",
		},
		{
			name:        "non-HTTPS URL (security check)",
			endpoint:    "http://example.com/auth",
			expectError: true,
			errorMsg:    "endpoint must use HTTPS",
		},
		{
			name:        "valid HTTPS URL",
			endpoint:    "https://example.com/auth",
			expectError: false,
		},
		{
			name:        "localhost HTTP allowed",
			endpoint:    "http://localhost:8080/auth",
			expectError: false,
		},
		{
			name:        "127.0.0.1 HTTP allowed",
			endpoint:    "http://127.0.0.1:8080/auth",
			expectError: false,
		},
		{
			name:        "IPv6 localhost HTTP allowed",
			endpoint:    "http://[::1]:8080/auth",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEndpointURL(tt.endpoint)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsLocalhost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		host     string
		expected bool
	}{
		{"localhost", "localhost", true},
		{"localhost with port", "localhost:8080", true},
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.1 with port", "127.0.0.1:8080", true},
		{"IPv6 localhost", "[::1]", true},
		{"IPv6 localhost with port", "[::1]:8080", true},
		{"remote host", "example.com", false},
		{"remote host with port", "example.com:443", false},
		{"other IP", "192.168.1.1", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isLocalhost(tt.host)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// testCreateOAuthConfigFromOIDC is a test version that uses our test discovery function
func testCreateOAuthConfigFromOIDC(
	ctx context.Context,
	t *testing.T,
	issuer, clientID, clientSecret string,
	scopes []string,
	usePKCE bool,
	callbackPort int,
) (*Config, error) {
	t.Helper()

	// Discover OIDC endpoints using our test function
	doc, err := testDiscoverOIDCEndpoints(ctx, t, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC endpoints: %w", err)
	}

	// Use default scopes if none provided
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}

	// Enable PKCE if the server supports it (S256 method)
	supportsPKCE := false
	for _, method := range doc.CodeChallengeMethodsSupported {
		if method == "S256" {
			supportsPKCE = true
			break
		}
	}

	// Enable PKCE if explicitly requested or if server supports it
	if usePKCE || supportsPKCE {
		usePKCE = true
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

func TestCreateOAuthConfigFromOIDC(t *testing.T) {
	t.Parallel()
	// Create a test server that serves OIDC discovery
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		doc := OIDCDiscoveryDocument{
			Issuer:                        server.URL,
			AuthorizationEndpoint:         server.URL + "/auth",
			TokenEndpoint:                 server.URL + "/token",
			JWKSURI:                       server.URL + "/jwks",
			UserinfoEndpoint:              server.URL + "/userinfo",
			CodeChallengeMethodsSupported: []string{"S256", "plain"},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(server.Close)

	tests := []struct {
		name         string
		issuer       string
		clientID     string
		clientSecret string
		scopes       []string
		usePKCE      bool
		expectError  bool
		errorMsg     string
		validate     func(t *testing.T, config *Config)
	}{
		{
			name:         "valid config with default scopes",
			issuer:       server.URL,
			clientID:     "test-client",
			clientSecret: "test-secret",
			scopes:       nil, // Should use defaults
			usePKCE:      false,
			expectError:  false,
			validate: func(t *testing.T, config *Config) {
				t.Helper()
				assert.Equal(t, "test-client", config.ClientID)
				assert.Equal(t, "test-secret", config.ClientSecret)
				assert.Equal(t, server.URL+"/auth", config.AuthURL)
				assert.Equal(t, server.URL+"/token", config.TokenURL)
				assert.Equal(t, []string{"openid", "profile", "email"}, config.Scopes)
				assert.True(t, config.UsePKCE) // Should be enabled due to server support
			},
		},
		{
			name:         "valid config with custom scopes",
			issuer:       server.URL,
			clientID:     "test-client",
			clientSecret: "test-secret",
			scopes:       []string{"openid", "custom"},
			usePKCE:      true,
			expectError:  false,
			validate: func(t *testing.T, config *Config) {
				t.Helper()
				assert.Equal(t, []string{"openid", "custom"}, config.Scopes)
				assert.True(t, config.UsePKCE)
			},
		},
		{
			name:         "PKCE explicitly disabled",
			issuer:       server.URL,
			clientID:     "test-client",
			clientSecret: "test-secret",
			scopes:       []string{"openid"},
			usePKCE:      false,
			expectError:  false,
			validate: func(t *testing.T, config *Config) {
				t.Helper()
				// Should still be enabled due to server support
				assert.True(t, config.UsePKCE)
			},
		},
		{
			name:        "invalid issuer",
			issuer:      "https://nonexistent.example.com",
			clientID:    "test-client",
			expectError: true,
			errorMsg:    "failed to discover OIDC endpoints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			config, err := testCreateOAuthConfigFromOIDC(
				ctx,
				t,
				tt.issuer,
				tt.clientID,
				tt.clientSecret,
				tt.scopes,
				tt.usePKCE,
				0, // Use auto-select port for tests
			)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, config)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, config)

			if tt.validate != nil {
				tt.validate(t, config)
			}
		})
	}
}

func TestOIDCDiscovery_SecurityProperties(t *testing.T) {
	t.Parallel()
	t.Run("request timeout protection", func(t *testing.T) {
		t.Parallel()
		// Create a server that never responds
		server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second) // Simulate hanging server (shorter for tests)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := testDiscoverOIDCEndpoints(ctx, t, server.URL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("response size limit protection", func(t *testing.T) {
		t.Parallel()
		// Create a server that returns a very large response
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			// Write more than 1MB of data
			largeData := strings.Repeat("x", 2*1024*1024)
			w.Write([]byte(`{"issuer":"` + largeData + `"}`))
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := testDiscoverOIDCEndpoints(ctx, t, server.URL)
		require.Error(t, err)
		// The error should be related to the size limit
		assert.True(t, strings.Contains(err.Error(), "failed to decode") ||
			strings.Contains(err.Error(), "http: request body too large"))
	})

	t.Run("strict JSON parsing", func(t *testing.T) {
		t.Parallel()
		// Create a server that returns JSON with unknown fields
		var server *httptest.Server
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Include an unknown field that should cause strict parsing to fail
			response := `{
				"issuer": "` + server.URL + `",
				"authorization_endpoint": "` + server.URL + `/auth",
				"token_endpoint": "` + server.URL + `/token",
				"jwks_uri": "` + server.URL + `/jwks",
				"unknown_field": "should_cause_error"
			}`
			w.Write([]byte(response))
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := testDiscoverOIDCEndpoints(ctx, t, server.URL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode OIDC configuration")
	})

	t.Run("user agent header set", func(t *testing.T) {
		t.Parallel()
		userAgentReceived := ""
		var server *httptest.Server
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userAgentReceived = r.Header.Get("User-Agent")

			doc := OIDCDiscoveryDocument{
				Issuer:                server.URL,
				AuthorizationEndpoint: server.URL + "/auth",
				TokenEndpoint:         server.URL + "/token",
				JWKSURI:               server.URL + "/jwks",
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(doc)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := testDiscoverOIDCEndpoints(ctx, t, server.URL)
		require.NoError(t, err)
		assert.Equal(t, "ToolHive/1.0", userAgentReceived)
	})
}

func TestOIDCDiscovery_EdgeCases(t *testing.T) {
	t.Parallel()
	t.Run("issuer with trailing slash", func(t *testing.T) {
		t.Parallel()
		var server *httptest.Server
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify the path is correct even with trailing slash in issuer
			assert.Equal(t, "/.well-known/openid-configuration", r.URL.Path)

			doc := OIDCDiscoveryDocument{
				Issuer:                server.URL + "/", // Include trailing slash to match the request
				AuthorizationEndpoint: server.URL + "/auth",
				TokenEndpoint:         server.URL + "/token",
				JWKSURI:               server.URL + "/jwks",
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(doc)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Test with trailing slash
		_, err := testDiscoverOIDCEndpoints(ctx, t, server.URL+"/")
		assert.NoError(t, err)
	})

	t.Run("empty optional fields", func(t *testing.T) {
		t.Parallel()
		var server *httptest.Server
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			doc := OIDCDiscoveryDocument{
				Issuer:                server.URL,
				AuthorizationEndpoint: server.URL + "/auth",
				TokenEndpoint:         server.URL + "/token",
				JWKSURI:               server.URL + "/jwks",
				// UserinfoEndpoint is empty (optional)
				// CodeChallengeMethodsSupported is empty
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(doc)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		doc, err := testDiscoverOIDCEndpoints(ctx, t, server.URL)
		require.NoError(t, err)
		assert.Empty(t, doc.UserinfoEndpoint)
		assert.Empty(t, doc.CodeChallengeMethodsSupported)
	})
}

// Test the production DiscoverOIDCEndpoints function with mock client
func TestDiscoverOIDCEndpoints_Production(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		issuer         string
		serverResponse func() *httptest.Server
		expectError    bool
		errorMsg       string
		validate       func(t *testing.T, doc *OIDCDiscoveryDocument)
	}{
		{
			name:        "invalid issuer URL",
			issuer:      "not-a-url",
			expectError: true,
			errorMsg:    "issuer must use HTTPS",
		},
		{
			name:        "non-HTTPS issuer (security check)",
			issuer:      "http://example.com",
			expectError: true,
			errorMsg:    "issuer must use HTTPS",
		},
		{
			name:   "localhost HTTP allowed for development",
			issuer: "http://localhost:8080",
			serverResponse: func() *httptest.Server {
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/.well-known/openid-configuration" {
						t.Errorf("unexpected path: %s", r.URL.Path)
					}

					// Use the actual server URL but replace 127.0.0.1 with localhost
					issuerURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

					doc := OIDCDiscoveryDocument{
						Issuer:                        issuerURL,
						AuthorizationEndpoint:         issuerURL + "/auth",
						TokenEndpoint:                 issuerURL + "/token",
						JWKSURI:                       issuerURL + "/jwks",
						UserinfoEndpoint:              issuerURL + "/userinfo",
						CodeChallengeMethodsSupported: []string{"S256", "plain"},
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
				return server
			},
			expectError: false,
			validate: func(t *testing.T, doc *OIDCDiscoveryDocument) {
				t.Helper()
				assert.True(t, strings.HasPrefix(doc.Issuer, "http://localhost:"))
				assert.True(t, strings.HasSuffix(doc.AuthorizationEndpoint, "/auth"))
				assert.True(t, strings.HasSuffix(doc.TokenEndpoint, "/token"))
				assert.True(t, strings.HasSuffix(doc.JWKSURI, "/jwks"))
				assert.Contains(t, doc.CodeChallengeMethodsSupported, "S256")
			},
		},
		{
			name:   "valid HTTPS discovery",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				var server *httptest.Server
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/.well-known/openid-configuration", r.URL.Path)
					assert.Equal(t, "ToolHive/1.0", r.Header.Get("User-Agent"))
					assert.Equal(t, "application/json", r.Header.Get("Accept"))

					doc := OIDCDiscoveryDocument{
						Issuer:                        server.URL,
						AuthorizationEndpoint:         server.URL + "/auth",
						TokenEndpoint:                 server.URL + "/token",
						JWKSURI:                       server.URL + "/jwks",
						UserinfoEndpoint:              server.URL + "/userinfo",
						CodeChallengeMethodsSupported: []string{"S256"},
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
				return server
			},
			expectError: false,
			validate: func(t *testing.T, doc *OIDCDiscoveryDocument) {
				t.Helper()
				// The issuer should match the server URL
				assert.True(t, strings.HasPrefix(doc.Issuer, "https://127.0.0.1:"))
				assert.True(t, strings.HasSuffix(doc.AuthorizationEndpoint, "/auth"))
				assert.True(t, strings.HasSuffix(doc.TokenEndpoint, "/token"))
				assert.True(t, strings.HasSuffix(doc.JWKSURI, "/jwks"))
				assert.True(t, strings.HasSuffix(doc.UserinfoEndpoint, "/userinfo"))
			},
		},
		{
			name:   "server returns non-200 status",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: true,
			errorMsg:    "OIDC discovery endpoint returned status 404",
		},
		{
			name:   "server returns wrong content type",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte("<html>Not JSON</html>"))
				}))
			},
			expectError: true,
			errorMsg:    "unexpected content type",
		},
		{
			name:   "server returns invalid JSON",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte("invalid json"))
				}))
			},
			expectError: true,
			errorMsg:    "failed to decode OIDC configuration",
		},
		{
			name:   "missing required fields",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				var server *httptest.Server
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					doc := OIDCDiscoveryDocument{
						Issuer: server.URL,
						// Missing required fields
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
				return server
			},
			expectError: true,
			errorMsg:    "missing authorization_endpoint",
		},
		{
			name:   "issuer mismatch (security check)",
			issuer: "https://example.com",
			serverResponse: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					doc := OIDCDiscoveryDocument{
						Issuer:                "https://malicious.com", // Different issuer
						AuthorizationEndpoint: "https://malicious.com/auth",
						TokenEndpoint:         "https://malicious.com/token",
						JWKSURI:               "https://malicious.com/jwks",
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(doc)
				}))
			},
			expectError: true,
			errorMsg:    "issuer mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			issuer := tt.issuer

			if tt.serverResponse != nil {
				server = tt.serverResponse()
				defer server.Close()

				// Replace the issuer with the test server URL for tests that need a server
				if strings.Contains(tt.name, "localhost HTTP") {
					// For localhost test, replace the port but keep localhost
					issuer = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
				} else if strings.Contains(tt.name, "valid HTTPS discovery") ||
					strings.Contains(tt.name, "server returns") ||
					strings.Contains(tt.name, "missing required fields") ||
					strings.Contains(tt.name, "issuer mismatch") {
					issuer = server.URL
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Test the production function with TLS-skipping client for test servers
			var client httpClient
			if tt.serverResponse != nil {
				client = &http.Client{
					Timeout: 30 * time.Second,
					Transport: &http.Transport{
						TLSHandshakeTimeout:   10 * time.Second,
						ResponseHeaderTimeout: 10 * time.Second,
						TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
					},
				}
			}
			doc, err := discoverOIDCEndpointsWithClient(ctx, issuer, client)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, doc)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, doc)

			if tt.validate != nil {
				tt.validate(t, doc)
			}
		})
	}
}

// Test the production CreateOAuthConfigFromOIDC function
func TestCreateOAuthConfigFromOIDC_Production(t *testing.T) {
	t.Parallel()
	// Create a test server that serves OIDC discovery
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		doc := OIDCDiscoveryDocument{
			Issuer:                        server.URL,
			AuthorizationEndpoint:         server.URL + "/auth",
			TokenEndpoint:                 server.URL + "/token",
			JWKSURI:                       server.URL + "/jwks",
			UserinfoEndpoint:              server.URL + "/userinfo",
			CodeChallengeMethodsSupported: []string{"S256", "plain"},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(server.Close)

	tests := []struct {
		name         string
		issuer       string
		clientID     string
		clientSecret string
		scopes       []string
		usePKCE      bool
		expectError  bool
		errorMsg     string
		validate     func(t *testing.T, config *Config)
	}{
		{
			name:         "valid config with default scopes",
			issuer:       server.URL,
			clientID:     "test-client",
			clientSecret: "test-secret",
			scopes:       nil, // Should use defaults
			usePKCE:      false,
			expectError:  false,
			validate: func(t *testing.T, config *Config) {
				t.Helper()
				assert.Equal(t, "test-client", config.ClientID)
				assert.Equal(t, "test-secret", config.ClientSecret)
				assert.Equal(t, server.URL+"/auth", config.AuthURL)
				assert.Equal(t, server.URL+"/token", config.TokenURL)
				assert.Equal(t, []string{"openid", "profile", "email"}, config.Scopes)
				assert.True(t, config.UsePKCE) // Should be enabled due to server support
			},
		},
		{
			name:         "valid config with custom scopes",
			issuer:       server.URL,
			clientID:     "test-client",
			clientSecret: "test-secret",
			scopes:       []string{"openid", "custom"},
			usePKCE:      true,
			expectError:  false,
			validate: func(t *testing.T, config *Config) {
				t.Helper()
				assert.Equal(t, []string{"openid", "custom"}, config.Scopes)
				assert.True(t, config.UsePKCE)
			},
		},
		{
			name:         "PKCE explicitly disabled",
			issuer:       server.URL,
			clientID:     "test-client",
			clientSecret: "test-secret",
			scopes:       []string{"openid"},
			usePKCE:      false,
			expectError:  false,
			validate: func(t *testing.T, config *Config) {
				t.Helper()
				// Should still be enabled due to server support
				assert.True(t, config.UsePKCE)
			},
		},
		{
			name:        "invalid issuer",
			issuer:      "https://nonexistent.example.com",
			clientID:    "test-client",
			expectError: true,
			errorMsg:    "failed to discover OIDC endpoints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Test the production function with TLS-skipping client for test servers
			var client httpClient
			if tt.issuer == server.URL {
				client = &http.Client{
					Timeout: 30 * time.Second,
					Transport: &http.Transport{
						TLSHandshakeTimeout:   10 * time.Second,
						ResponseHeaderTimeout: 10 * time.Second,
						TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
					},
				}
			}
			config, err := createOAuthConfigFromOIDCWithClient(
				ctx,
				tt.issuer,
				tt.clientID,
				tt.clientSecret,
				tt.scopes,
				tt.usePKCE,
				0, // Use auto-select port for tests
				client,
			)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, config)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, config)

			if tt.validate != nil {
				tt.validate(t, config)
			}
		})
	}
}

func TestValidateEndpointURL_AdditionalCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		endpoint    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "URL with fragment (should be rejected)",
			endpoint:    "https://example.com/auth#fragment",
			expectError: false, // Fragments are allowed in URLs
		},
		{
			name:        "URL with query parameters",
			endpoint:    "https://example.com/auth?param=value",
			expectError: false,
		},
		{
			name:        "URL with port",
			endpoint:    "https://example.com:8443/auth",
			expectError: false,
		},
		{
			name:        "localhost with custom port",
			endpoint:    "http://localhost:3000/auth",
			expectError: false,
		},
		{
			name:        "127.0.0.1 with custom port",
			endpoint:    "http://127.0.0.1:3000/auth",
			expectError: false,
		},
		{
			name:        "IPv6 localhost with custom port",
			endpoint:    "http://[::1]:3000/auth",
			expectError: false,
		},
		{
			name:        "malformed URL with spaces",
			endpoint:    "https://example .com/auth",
			expectError: true,
			errorMsg:    "invalid URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEndpointURL(tt.endpoint)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

func TestMain(m *testing.M) {
	// Initialize logger for tests
	logger.Initialize()

	// Run tests
	code := m.Run()

	// Exit with the test result code
	os.Exit(code)
}

func TestNewFlow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		config      *Config
		expectError bool
		errorMsg    string
	}{
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
			errorMsg:    "OAuth config cannot be nil",
		},
		{
			name: "missing client ID",
			config: &Config{
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
			},
			expectError: true,
			errorMsg:    "client ID is required",
		},
		{
			name: "missing auth URL",
			config: &Config{
				ClientID: "test-client",
				TokenURL: "https://example.com/token",
			},
			expectError: true,
			errorMsg:    "authorization URL is required",
		},
		{
			name: "missing token URL",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
			},
			expectError: true,
			errorMsg:    "token URL is required",
		},
		{
			name: "valid config without PKCE",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
				Scopes:   []string{"openid", "profile"},
			},
			expectError: false,
		},
		{
			name: "valid config with PKCE",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
				Scopes:   []string{"openid", "profile"},
				UsePKCE:  true,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flow, err := NewFlow(tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, flow)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, flow)

			// Verify PKCE parameters are generated when enabled
			if tt.config.UsePKCE {
				assert.NotEmpty(t, flow.codeVerifier, "code verifier should be generated")
				assert.NotEmpty(t, flow.codeChallenge, "code challenge should be generated")

				// Verify code verifier is valid base64
				decoded, err := base64.RawURLEncoding.DecodeString(flow.codeVerifier)
				require.NoError(t, err, "code verifier should be valid base64")
				assert.Len(t, decoded, 32, "code verifier should be 32 bytes when decoded")

				// Verify code challenge is valid base64
				_, err = base64.RawURLEncoding.DecodeString(flow.codeChallenge)
				assert.NoError(t, err, "code challenge should be valid base64")
			}

			// Verify state parameter is generated and valid
			assert.NotEmpty(t, flow.state, "state parameter should be generated")
			decoded, err := base64.RawURLEncoding.DecodeString(flow.state)
			require.NoError(t, err, "state parameter should be valid base64")
			assert.Len(t, decoded, 16, "state should be 16 bytes when decoded")

			// Verify port is assigned
			assert.Greater(t, flow.port, 0, "port should be assigned")

			// Verify OAuth2 config is properly set
			assert.Equal(t, tt.config.ClientID, flow.oauth2Config.ClientID)
			assert.Equal(t, tt.config.ClientSecret, flow.oauth2Config.ClientSecret)
			assert.Equal(t, tt.config.Scopes, flow.oauth2Config.Scopes)
		})
	}
}

func TestGeneratePKCEParams(t *testing.T) {
	t.Parallel()
	flow := &Flow{}

	err := flow.generatePKCEParams()
	require.NoError(t, err)

	// Verify code verifier is generated and valid
	assert.NotEmpty(t, flow.codeVerifier)
	decoded, err := base64.RawURLEncoding.DecodeString(flow.codeVerifier)
	require.NoError(t, err, "code verifier should be valid base64")
	assert.Len(t, decoded, 32, "code verifier should be 32 bytes when decoded")

	// Verify code challenge is generated and valid
	assert.NotEmpty(t, flow.codeChallenge)
	_, err = base64.RawURLEncoding.DecodeString(flow.codeChallenge)
	require.NoError(t, err, "code challenge should be valid base64")

	// Verify code challenge is correctly computed (S256 method)
	hash := sha256.Sum256([]byte(flow.codeVerifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
	assert.Equal(t, expectedChallenge, flow.codeChallenge, "code challenge should be S256 hash of verifier")

	// Test that multiple calls generate different values (security requirement)
	originalVerifier := flow.codeVerifier
	originalChallenge := flow.codeChallenge

	err = flow.generatePKCEParams()
	require.NoError(t, err)

	assert.NotEqual(t, originalVerifier, flow.codeVerifier, "code verifier should be different on each call")
	assert.NotEqual(t, originalChallenge, flow.codeChallenge, "code challenge should be different on each call")
}

func TestGenerateState(t *testing.T) {
	t.Parallel()
	flow := &Flow{}

	err := flow.generateState()
	require.NoError(t, err)

	// Verify state is generated and valid
	assert.NotEmpty(t, flow.state)
	decoded, err := base64.RawURLEncoding.DecodeString(flow.state)
	require.NoError(t, err, "state should be valid base64")
	assert.Len(t, decoded, 16, "state should be 16 bytes when decoded")

	// Test that multiple calls generate different values (security requirement)
	originalState := flow.state

	err = flow.generateState()
	require.NoError(t, err)

	assert.NotEqual(t, originalState, flow.state, "state should be different on each call")
}

func TestBuildAuthURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		config   *Config
		usePKCE  bool
		validate func(t *testing.T, authURL string, flow *Flow)
	}{
		{
			name: "basic auth URL without PKCE",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
				Scopes:   []string{"openid", "profile"},
			},
			usePKCE: false,
			validate: func(t *testing.T, authURL string, flow *Flow) {
				t.Helper()
				parsedURL, err := url.Parse(authURL)
				require.NoError(t, err)

				assert.Equal(t, "https", parsedURL.Scheme)
				assert.Equal(t, "example.com", parsedURL.Host)
				assert.Equal(t, "/auth", parsedURL.Path)

				query := parsedURL.Query()
				assert.Equal(t, "test-client", query.Get("client_id"))
				assert.Equal(t, "code", query.Get("response_type"))
				assert.Equal(t, flow.state, query.Get("state"))
				assert.Contains(t, query.Get("scope"), "openid")
				assert.Contains(t, query.Get("scope"), "profile")

				// Should not have PKCE parameters
				assert.Empty(t, query.Get("code_challenge"))
				assert.Empty(t, query.Get("code_challenge_method"))
			},
		},
		{
			name: "auth URL with PKCE",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
				Scopes:   []string{"openid", "profile"},
				UsePKCE:  true,
			},
			usePKCE: true,
			validate: func(t *testing.T, authURL string, flow *Flow) {
				t.Helper()
				parsedURL, err := url.Parse(authURL)
				require.NoError(t, err)

				query := parsedURL.Query()
				assert.Equal(t, flow.codeChallenge, query.Get("code_challenge"))
				assert.Equal(t, "S256", query.Get("code_challenge_method"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flow, err := NewFlow(tt.config)
			require.NoError(t, err)

			authURL := flow.buildAuthURL()
			assert.NotEmpty(t, authURL)

			tt.validate(t, authURL, flow)
		})
	}
}

func TestHandleCallback_SecurityValidation(t *testing.T) {
	t.Parallel()
	config := &Config{
		ClientID: "test-client",
		AuthURL:  "https://example.com/auth",
		TokenURL: "https://example.com/token",
		UsePKCE:  true,
	}

	flow, err := NewFlow(config)
	require.NoError(t, err)

	tokenChan := make(chan *oauth2.Token, 1)
	errorChan := make(chan error, 1)

	handler := flow.handleCallback(tokenChan, errorChan)

	tests := []struct {
		name           string
		queryParams    map[string]string
		expectError    bool
		expectedErrMsg string
	}{
		{
			name: "OAuth error response",
			queryParams: map[string]string{
				"error":             "access_denied",
				"error_description": "User denied access",
			},
			expectError:    true,
			expectedErrMsg: "OAuth error: access_denied - User denied access",
		},
		{
			name: "invalid state parameter",
			queryParams: map[string]string{
				"state": "invalid-state",
				"code":  "test-code",
			},
			expectError:    true,
			expectedErrMsg: "invalid state parameter",
		},
		{
			name: "missing authorization code",
			queryParams: map[string]string{
				"state": flow.state,
			},
			expectError:    true,
			expectedErrMsg: "missing authorization code",
		},
		{
			name: "empty authorization code",
			queryParams: map[string]string{
				"state": flow.state,
				"code":  "",
			},
			expectError:    true,
			expectedErrMsg: "missing authorization code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Build query string
			values := url.Values{}
			for k, v := range tt.queryParams {
				values.Set(k, v)
			}

			req := httptest.NewRequest("GET", "/callback?"+values.Encode(), nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if tt.expectError {
				select {
				case err := <-errorChan:
					assert.Contains(t, err.Error(), tt.expectedErrMsg)
				case <-time.After(100 * time.Millisecond):
					t.Error("expected error but none received")
				}
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	flow := &Flow{}
	w := httptest.NewRecorder()

	flow.setSecurityHeaders(w)

	headers := w.Header()

	// Test all security headers are set
	assert.Equal(t, "text/html; charset=utf-8", headers.Get("Content-Type"))
	assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", headers.Get("X-Frame-Options"))
	assert.Equal(t, "1; mode=block", headers.Get("X-XSS-Protection"))
	assert.Equal(t, "strict-origin-when-cross-origin", headers.Get("Referrer-Policy"))

	csp := headers.Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "script-src 'none'")
	assert.Contains(t, csp, "object-src 'none'")
}

func TestHandleRoot_SecurityValidation(t *testing.T) {
	t.Parallel()
	flow := &Flow{}
	handler := flow.handleRoot()

	tests := []struct {
		name           string
		method         string
		expectedStatus int
	}{
		{
			name:           "GET request allowed",
			method:         "GET",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "POST request blocked",
			method:         "POST",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT request blocked",
			method:         "PUT",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE request blocked",
			method:         "DELETE",
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tt.method, "/", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				// Verify security headers are set
				assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
				assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))

				// Verify HTML content is safe
				body := w.Body.String()
				assert.Contains(t, body, "ToolHive OAuth Authentication")
				assert.NotContains(t, body, "<script>") // No inline scripts
			}
		})
	}
}

func TestWriteErrorPage_XSSPrevention(t *testing.T) {
	t.Parallel()
	flow := &Flow{}
	w := httptest.NewRecorder()

	// Test with potentially malicious error message
	maliciousError := fmt.Errorf("<script>alert('xss')</script>")

	flow.writeErrorPage(w, maliciousError)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Verify security headers
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))

	body := w.Body.String()

	// Verify XSS is prevented - script tags should be escaped
	assert.NotContains(t, body, "<script>alert('xss')</script>")
	assert.Contains(t, body, "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;")

	// Verify error page structure
	assert.Contains(t, body, "Authentication Failed")
	assert.Contains(t, body, "<!DOCTYPE html>")
}

func TestProcessToken(t *testing.T) {
	t.Parallel()
	// Create a proper flow with config to avoid nil pointer issues
	config := &Config{
		ClientID: "test-client",
		AuthURL:  "https://example.com/auth",
		TokenURL: "https://example.com/token",
	}

	flow, err := NewFlow(config)
	require.NoError(t, err)

	// Test with a valid OAuth2 token
	token := &oauth2.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}

	result := flow.processToken(token)

	assert.NotNil(t, result)
	assert.Equal(t, token.AccessToken, result.AccessToken)
	assert.Equal(t, token.RefreshToken, result.RefreshToken)
	assert.Equal(t, token.TokenType, result.TokenType)
	assert.Equal(t, token.Expiry, result.Expiry)
}

func TestExtractJWTClaims(t *testing.T) {
	t.Parallel()
	flow := &Flow{}

	tests := []struct {
		name        string
		token       string
		expectError bool
	}{
		{
			name:        "invalid JWT",
			token:       "invalid.jwt.token",
			expectError: true,
		},
		{
			name:        "empty token",
			token:       "",
			expectError: true,
		},
		{
			name:        "non-JWT token (opaque)",
			token:       "opaque-access-token-12345",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			claims, err := flow.extractJWTClaims(tt.token)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, claims)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, claims)
			}
		})
	}

	// Test with a valid JWT (unsigned for testing)
	t.Run("valid JWT", func(t *testing.T) {
		t.Parallel()
		// Create a test JWT
		claims := jwt.MapClaims{
			"sub":   "1234567890",
			"name":  "John Doe",
			"email": "john@example.com",
			"iat":   time.Now().Unix(),
		}

		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		extractedClaims, err := flow.extractJWTClaims(tokenString)
		assert.NoError(t, err)
		assert.NotNil(t, extractedClaims)
		assert.Equal(t, "1234567890", extractedClaims["sub"])
		assert.Equal(t, "John Doe", extractedClaims["name"])
		assert.Equal(t, "john@example.com", extractedClaims["email"])
	})
}

func TestPKCESecurityProperties(t *testing.T) {
	t.Parallel()
	// Test that PKCE parameters have sufficient entropy
	flow := &Flow{}

	// Generate multiple PKCE parameters and ensure they're all different
	verifiers := make(map[string]bool)
	challenges := make(map[string]bool)

	for i := 0; i < 100; i++ {
		err := flow.generatePKCEParams()
		require.NoError(t, err)

		// Ensure no duplicates (extremely unlikely with proper randomness)
		assert.False(t, verifiers[flow.codeVerifier], "code verifier should be unique")
		assert.False(t, challenges[flow.codeChallenge], "code challenge should be unique")

		verifiers[flow.codeVerifier] = true
		challenges[flow.codeChallenge] = true

		// Verify length requirements (RFC 7636)
		assert.GreaterOrEqual(t, len(flow.codeVerifier), 43, "code verifier should be at least 43 characters")
		assert.LessOrEqual(t, len(flow.codeVerifier), 128, "code verifier should be at most 128 characters")
	}
}

func TestStateSecurityProperties(t *testing.T) {
	t.Parallel()
	// Test that state parameters have sufficient entropy
	flow := &Flow{}

	// Generate multiple state parameters and ensure they're all different
	states := make(map[string]bool)

	for i := 0; i < 100; i++ {
		err := flow.generateState()
		require.NoError(t, err)

		// Ensure no duplicates (extremely unlikely with proper randomness)
		assert.False(t, states[flow.state], "state should be unique")
		states[flow.state] = true

		// Verify state is not empty and has reasonable length
		assert.NotEmpty(t, flow.state)
		assert.GreaterOrEqual(t, len(flow.state), 16, "state should have sufficient length")
	}
}

func TestStart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		config      *Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "successful OAuth flow start",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
				Scopes:   []string{"openid", "profile"},
			},
			expectError: false,
		},
		{
			name: "OAuth flow start with PKCE",
			config: &Config{
				ClientID: "test-client",
				AuthURL:  "https://example.com/auth",
				TokenURL: "https://example.com/token",
				Scopes:   []string{"openid", "profile"},
				UsePKCE:  true,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flow, err := NewFlow(tt.config)
			require.NoError(t, err)

			// Generate the auth URL before starting the flow
			authURL := flow.buildAuthURL()

			// Verify the auth URL was generated correctly
			assert.NotEmpty(t, authURL, "auth URL should be generated")
			assert.Contains(t, authURL, "https://example.com/auth", "auth URL should contain the authorization endpoint")
			assert.Contains(t, authURL, "client_id=test-client", "auth URL should contain client ID")
			assert.Contains(t, authURL, "response_type=code", "auth URL should contain response type")

			// Start the OAuth flow in a goroutine since it blocks
			done := make(chan struct{})
			var startErr error

			go func() {
				defer close(done)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, startErr = flow.Start(ctx, true)
			}()

			// Give the server a moment to start
			time.Sleep(100 * time.Millisecond)

			if tt.expectError {
				// Cancel the flow and wait for completion
				select {
				case <-done:
					require.Error(t, startErr)
					assert.Contains(t, startErr.Error(), tt.errorMsg)
				case <-time.After(1 * time.Second):
					t.Error("Start() should have returned an error quickly")
				}
				return
			}

			// Simulate user completing OAuth flow by making a callback request
			callbackURL := fmt.Sprintf("http://localhost:%d/callback?code=test-code&state=%s", flow.port, flow.state)

			// Make the callback request
			resp, err := http.Get(callbackURL)
			if err == nil {
				resp.Body.Close()
			}

			// Wait for the flow to complete or timeout
			select {
			case <-done:
				// The flow should complete, but we expect an error since we're using a fake token endpoint
				assert.Error(t, startErr, "should get error from fake token endpoint")
			case <-time.After(2 * time.Second):
				t.Error("Start() should have completed within timeout")
			}
		})
	}
}

func TestWriteSuccessPage(t *testing.T) {
	t.Parallel()
	flow := &Flow{}
	w := httptest.NewRecorder()

	flow.writeSuccessPage(w)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify security headers
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))

	body := w.Body.String()

	// Verify success page structure
	assert.Contains(t, body, "Authentication Successful")
	assert.Contains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, "You can now close this window")

	// Verify no sensitive information is exposed
	assert.NotContains(t, body, "test-access-token")
	assert.NotContains(t, body, "test-refresh-token")

	// Verify no inline scripts for security
	assert.NotContains(t, body, "<script>")
}

func TestHandleCallback_SuccessfulFlow(t *testing.T) {
	t.Parallel()
	// Create a mock token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		// Parse form data
		err := r.ParseForm()
		require.NoError(t, err)

		assert.Equal(t, "authorization_code", r.Form.Get("grant_type"))
		assert.Equal(t, "test-code", r.Form.Get("code"))

		// Client ID might be in form data or Authorization header depending on OAuth2 library implementation
		clientID := r.Form.Get("client_id")
		if clientID == "" {
			// Check Authorization header for Basic auth
			username, _, ok := r.BasicAuth()
			if ok {
				clientID = username
			}
		}
		assert.Equal(t, "test-client", clientID, "client_id should be present in form data or Authorization header")

		// Return a valid token response
		response := map[string]interface{}{
			"access_token":  "test-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "test-refresh-token",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer tokenServer.Close()

	config := &Config{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		AuthURL:      "https://example.com/auth",
		TokenURL:     tokenServer.URL,
		UsePKCE:      true,
	}

	flow, err := NewFlow(config)
	require.NoError(t, err)

	tokenChan := make(chan *oauth2.Token, 1)
	errorChan := make(chan error, 1)

	handler := flow.handleCallback(tokenChan, errorChan)

	// Build callback URL with valid parameters
	values := url.Values{}
	values.Set("code", "test-code")
	values.Set("state", flow.state)

	req := httptest.NewRequest("GET", "/callback?"+values.Encode(), nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should get a successful response
	assert.Equal(t, http.StatusOK, w.Code)

	// Should receive a token
	select {
	case token := <-tokenChan:
		assert.NotNil(t, token)
		assert.Equal(t, "test-access-token", token.AccessToken)
		assert.Equal(t, "Bearer", token.TokenType)
		assert.Equal(t, "test-refresh-token", token.RefreshToken)
	case err := <-errorChan:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("expected token but got timeout")
	}
}

func TestProcessToken_WithJWTClaims(t *testing.T) {
	t.Parallel()
	config := &Config{
		ClientID: "test-client",
		AuthURL:  "https://example.com/auth",
		TokenURL: "https://example.com/token",
	}

	flow, err := NewFlow(config)
	require.NoError(t, err)

	// Create a test JWT token
	claims := jwt.MapClaims{
		"sub":   "1234567890",
		"name":  "John Doe",
		"email": "john@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	}

	jwtToken := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenString, err := jwtToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	// Test with JWT access token
	token := &oauth2.Token{
		AccessToken:  tokenString,
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}

	result := flow.processToken(token)

	assert.NotNil(t, result)
	assert.Equal(t, tokenString, result.AccessToken)
	assert.Equal(t, token.RefreshToken, result.RefreshToken)
	assert.Equal(t, token.TokenType, result.TokenType)
	assert.Equal(t, token.Expiry, result.Expiry)

	// Verify JWT claims were extracted (this would be logged in real implementation)
	extractedClaims, err := flow.extractJWTClaims(tokenString)
	assert.NoError(t, err)
	assert.Equal(t, "1234567890", extractedClaims["sub"])
	assert.Equal(t, "John Doe", extractedClaims["name"])
	assert.Equal(t, "john@example.com", extractedClaims["email"])
}

func TestProcessToken_WithOpaqueToken(t *testing.T) {
	t.Parallel()
	config := &Config{
		ClientID: "test-client",
		AuthURL:  "https://example.com/auth",
		TokenURL: "https://example.com/token",
	}

	flow, err := NewFlow(config)
	require.NoError(t, err)

	// Test with opaque access token
	token := &oauth2.Token{
		AccessToken:  "opaque-access-token-12345",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}

	result := flow.processToken(token)

	assert.NotNil(t, result)
	assert.Equal(t, token.AccessToken, result.AccessToken)
	assert.Equal(t, token.RefreshToken, result.RefreshToken)
	assert.Equal(t, token.TokenType, result.TokenType)
	assert.Equal(t, token.Expiry, result.Expiry)
}

func TestExtractJWTClaims_ErrorCases(t *testing.T) {
	t.Parallel()
	flow := &Flow{}

	tests := []struct {
		name        string
		token       string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "malformed JWT - too few parts",
			token:       "invalid.jwt",
			expectError: true,
			errorMsg:    "token contains an invalid number of segments",
		},
		{
			name:        "malformed JWT - invalid base64",
			token:       "invalid.base64!.token",
			expectError: true,
			errorMsg:    "token is malformed",
		},
		{
			name:        "JWT with invalid JSON claims",
			token:       "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.invalid-json.signature",
			expectError: true,
			errorMsg:    "token is malformed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			claims, err := flow.extractJWTClaims(tt.token)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, claims)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, claims)
			}
		})
	}
}

// Package oauth provides OAuth 2.0 and OIDC authentication functionality.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pkg/browser"
	"golang.org/x/oauth2"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/networking"
)

// Config contains configuration for OAuth authentication
type Config struct {
	// ClientID is the OAuth client ID
	ClientID string

	// ClientSecret is the OAuth client secret (optional for PKCE flow)
	ClientSecret string

	// RedirectURL is the redirect URL for the OAuth flow
	RedirectURL string

	// AuthURL is the authorization endpoint URL
	AuthURL string

	// TokenURL is the token endpoint URL
	TokenURL string

	// Scopes are the OAuth scopes to request
	Scopes []string

	// UsePKCE enables PKCE (Proof Key for Code Exchange) for enhanced security
	UsePKCE bool

	// CallbackPort is the port for the OAuth callback server (optional, 0 means auto-select)
	CallbackPort int
}

// Flow handles the OAuth authentication flow
type Flow struct {
	config       *Config
	oauth2Config *oauth2.Config
	server       *http.Server
	port         int

	// PKCE parameters
	codeVerifier  string
	codeChallenge string
	state         string

	tokenSource oauth2.TokenSource
}

// TokenResult contains the result of the OAuth flow
type TokenResult struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Expiry       time.Time
	Claims       jwt.MapClaims
	IDToken      string // The OIDC ID token (JWT), if present
}

// NewFlow creates a new OAuth flow
func NewFlow(config *Config) (*Flow, error) {
	if config == nil {
		return nil, errors.New("OAuth config cannot be nil")
	}

	if config.ClientID == "" {
		return nil, errors.New("client ID is required")
	}

	if config.AuthURL == "" {
		return nil, errors.New("authorization URL is required")
	}

	if config.TokenURL == "" {
		return nil, errors.New("token URL is required")
	}

	// Use specified callback port or find an available port for the local server
	port, err := networking.FindOrUsePort(config.CallbackPort)
	if err != nil {
		return nil, fmt.Errorf("failed to find available port: %w", err)
	}

	// Set default redirect URL if not provided
	redirectURL := config.RedirectURL
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("http://localhost:%d/callback", port)
	}

	// Create OAuth2 config
	oauth2Config := &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       config.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  config.AuthURL,
			TokenURL: config.TokenURL,
		},
	}

	flow := &Flow{
		config:       config,
		oauth2Config: oauth2Config,
		port:         port,
	}

	// Generate PKCE parameters if enabled
	if config.UsePKCE {
		if err := flow.generatePKCEParams(); err != nil {
			return nil, fmt.Errorf("failed to generate PKCE parameters: %w", err)
		}
	}

	// Generate state parameter
	if err := flow.generateState(); err != nil {
		return nil, fmt.Errorf("failed to generate state parameter: %w", err)
	}

	return flow, nil
}

// generatePKCEParams generates PKCE code verifier and challenge
func (f *Flow) generatePKCEParams() error {
	// Generate code verifier (43-128 characters, RFC 7636)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return fmt.Errorf("failed to generate code verifier: %w", err)
	}
	f.codeVerifier = base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Use S256 method for enhanced security (RFC 7636 recommendation)
	hash := sha256.Sum256([]byte(f.codeVerifier))
	f.codeChallenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return nil
}

// generateState generates a random state parameter
func (f *Flow) generateState() error {
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}
	f.state = base64.RawURLEncoding.EncodeToString(stateBytes)
	return nil
}

// Start starts the OAuth authentication flow
func (f *Flow) Start(ctx context.Context, skipBrowser bool) (*TokenResult, error) {
	// Create channels for communication
	tokenChan := make(chan *oauth2.Token, 1)
	errorChan := make(chan error, 1)

	// Set up HTTP server for handling the callback
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", f.handleCallback(tokenChan, errorChan))
	mux.HandleFunc("/", f.handleRoot())

	f.server = &http.Server{
		Addr:              fmt.Sprintf(":%d", f.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start the server in a goroutine
	go func() {
		logger.Infof("Starting OAuth callback server on port %d", f.port)
		if err := f.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorChan <- fmt.Errorf("failed to start callback server: %w", err)
		}
	}()

	// Ensure server cleanup
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.server.Shutdown(shutdownCtx); err != nil {
			logger.Warnf("Failed to shutdown OAuth callback server: %v", err)
		}
	}()

	// Build authorization URL
	authURL := f.buildAuthURL()

	// Open browser or display URL
	if !skipBrowser {
		logger.Infof("Opening browser to: %s", authURL)
		if err := browser.OpenURL(authURL); err != nil {
			logger.Warnf("Failed to open browser: %v", err)
			logger.Infof("Please manually open this URL in your browser: %s", authURL)
		}
	} else {
		logger.Infof("Please open this URL in your browser: %s", authURL)
	}

	logger.Info("Waiting for OAuth callback...")

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for token, error, or cancellation
	select {
	case token := <-tokenChan:
		logger.Info("OAuth flow completed successfully")
		return f.processToken(token), nil
	case err := <-errorChan:
		return nil, fmt.Errorf("OAuth flow failed: %w", err)
	case <-ctx.Done():
		return nil, fmt.Errorf("OAuth flow cancelled: %w", ctx.Err())
	case sig := <-sigChan:
		return nil, fmt.Errorf("OAuth flow interrupted by signal: %v", sig)
	}
}

// buildAuthURL builds the authorization URL with appropriate parameters
func (f *Flow) buildAuthURL() string {
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("state", f.state),
	}

	// Add PKCE parameters if enabled
	if f.config.UsePKCE {
		opts = append(opts,
			oauth2.SetAuthURLParam("code_challenge", f.codeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
	}

	return f.oauth2Config.AuthCodeURL(f.state, opts...)
}

// handleCallback handles the OAuth callback
func (f *Flow) handleCallback(tokenChan chan<- *oauth2.Token, errorChan chan<- error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse query parameters
		query := r.URL.Query()

		// Check for error
		if errParam := query.Get("error"); errParam != "" {
			errDesc := query.Get("error_description")
			err := fmt.Errorf("OAuth error: %s - %s", errParam, errDesc)
			f.writeErrorPage(w, err)
			errorChan <- err
			return
		}

		// Validate state parameter
		state := query.Get("state")
		if state != f.state {
			err := errors.New("invalid state parameter")
			f.writeErrorPage(w, err)
			errorChan <- err
			return
		}

		// Get authorization code
		code := query.Get("code")
		if code == "" {
			err := errors.New("missing authorization code")
			f.writeErrorPage(w, err)
			errorChan <- err
			return
		}

		// Exchange code for token
		ctx := context.Background()
		opts := []oauth2.AuthCodeOption{}

		// Add PKCE verifier if enabled
		if f.config.UsePKCE {
			opts = append(opts, oauth2.SetAuthURLParam("code_verifier", f.codeVerifier))
		}

		token, err := f.oauth2Config.Exchange(ctx, code, opts...)
		if err != nil {
			err = fmt.Errorf("failed to exchange code for token: %w", err)
			f.writeErrorPage(w, err)
			errorChan <- err
			return
		}

		// Write success page
		f.writeSuccessPage(w)

		// Send token
		tokenChan <- token
	}
}

// setSecurityHeaders sets common security headers for all responses
func (*Flow) setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'none'; object-src 'none';")
}

// handleRoot handles requests to the root path
func (f *Flow) handleRoot() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow GET requests
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		f.setSecurityHeaders(w)
		htmlContent := `
<!DOCTYPE html>
<html>
<head>
    <title>ToolHive OAuth</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; text-align: center; }
        .container { max-width: 600px; margin: 0 auto; }
        .message { padding: 20px; border-radius: 5px; margin: 20px 0; }
        .info { background-color: #e7f3ff; border: 1px solid #b3d9ff; color: #0066cc; }
    </style>
</head>
<body>
    <div class="container">
        <h1>ToolHive OAuth Authentication</h1>
        <div class="message info">
            <p>OAuth callback server is running. Please complete the authentication flow in your browser.</p>
        </div>
    </div>
</body>
</html>`
		if _, err := w.Write([]byte(htmlContent)); err != nil {
			logger.Warnf("Failed to write HTML content: %v", err)
		}
	}
}

// writeSuccessPage writes a success page to the response
func (f *Flow) writeSuccessPage(w http.ResponseWriter) {
	f.setSecurityHeaders(w)
	htmlContent := `
<!DOCTYPE html>
<html>
<head>
    <title>Authentication Successful</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; text-align: center; }
        .container { max-width: 600px; margin: 0 auto; }
        .message { padding: 20px; border-radius: 5px; margin: 20px 0; }
        .success { background-color: #e7f6e7; border: 1px solid #b3e6b3; color: #006600; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Authentication Successful!</h1>
        <div class="message success">
            <p>You have successfully authenticated with ToolHive. You can now close this window and return to the terminal.</p>
        </div>
    </div>
</body>
</html>`
	if _, err := w.Write([]byte(htmlContent)); err != nil {
		logger.Warnf("Failed to write HTML content: %v", err)
	}
}

// writeErrorPage writes an error page to the response
func (*Flow) writeErrorPage(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.WriteHeader(http.StatusBadRequest)

	// HTML escape the error message to prevent XSS
	escapedError := html.EscapeString(err.Error())
	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <title>Authentication Failed</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; text-align: center; }
        .container { max-width: 600px; margin: 0 auto; }
        .message { padding: 20px; border-radius: 5px; margin: 20px 0; }
        .error { background-color: #ffe7e7; border: 1px solid #ffb3b3; color: #cc0000; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Authentication Failed</h1>
        <div class="message error">
            <p>%s</p>
            <p>Please try again or contact support if the problem persists.</p>
        </div>
    </div>
</body>
</html>`, escapedError)
	if _, err := w.Write([]byte(htmlContent)); err != nil {
		logger.Warnf("Failed to write HTML content: %v", err)
	}
}

// processToken processes the received token and extracts claims
func (f *Flow) processToken(token *oauth2.Token) *TokenResult {
	result := &TokenResult{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
	}

	// Create a base token source using the original token
	base := f.oauth2Config.TokenSource(context.Background(), token)

	// ReuseTokenSource ensures that refresh happens only when needed
	f.tokenSource = oauth2.ReuseTokenSource(token, base)

	// Prefer extracting claims from the ID token if present (OIDC, e.g., Google)
	if idToken, ok := token.Extra("id_token").(string); ok && idToken != "" {
		result.IDToken = idToken
		if claims, err := f.extractJWTClaims(idToken); err == nil {
			result.Claims = claims
			logger.Debugf("Successfully extracted JWT claims from ID token")
		} else {
			logger.Debugf("Could not extract JWT claims from ID token: %v", err)
		}
	} else {
		// Fallback: try to extract claims from the access token (e.g., Keycloak)
		if claims, err := f.extractJWTClaims(token.AccessToken); err == nil {
			result.Claims = claims
			logger.Debugf("Successfully extracted JWT claims from access token")
		} else {
			logger.Debugf("Could not extract JWT claims from access token (may be opaque token): %v", err)
		}
	}

	return result
}

// TokenSource returns the OAuth2 token source for refreshing tokens
func (f *Flow) TokenSource() oauth2.TokenSource {
	return f.tokenSource
}

// extractJWTClaims attempts to extract claims from a JWT token without validation
func (*Flow) extractJWTClaims(tokenString string) (jwt.MapClaims, error) {
	// Parse without verification to extract claims
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("failed to extract claims")
	}

	return claims, nil
}

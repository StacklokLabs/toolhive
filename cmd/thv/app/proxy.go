package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/stacklok/toolhive/pkg/auth"
	"github.com/stacklok/toolhive/pkg/auth/oauth"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/networking"
	"github.com/stacklok/toolhive/pkg/transport"
	"github.com/stacklok/toolhive/pkg/transport/proxy/transparent"
	"github.com/stacklok/toolhive/pkg/transport/types"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy [flags] SERVER_NAME",
	Short: "Create a transparent proxy for an MCP server with authentication support",
	Long: `Create a transparent HTTP proxy that forwards requests to an MCP server endpoint.
This command starts a standalone proxy without launching a container, providing:

• Transparent request forwarding to the target MCP server
• Optional OAuth/OIDC authentication to remote MCP servers
• Automatic authentication detection via WWW-Authenticate headers
• OIDC-based access control for incoming proxy requests
• Secure credential handling via files or environment variables

AUTHENTICATION MODES:
The proxy supports multiple authentication scenarios:

1. No Authentication: Simple transparent forwarding
2. Outgoing Authentication: Authenticate to remote MCP servers using OAuth/OIDC
3. Incoming Authentication: Protect the proxy endpoint with OIDC validation
4. Bidirectional: Both incoming and outgoing authentication

OAUTH CLIENT SECRET SOURCES:
OAuth client secrets can be provided via (in order of precedence):
1. --remote-auth-client-secret flag (not recommended for production)
2. --remote-auth-client-secret-file flag (secure file-based approach)
3. ` + envOAuthClientSecret + ` environment variable

EXAMPLES:
  # Basic transparent proxy
  thv proxy my-server --target-uri http://localhost:8080

  # Proxy with OAuth authentication to remote server
  thv proxy my-server --target-uri https://api.example.com \
    --remote-auth --remote-auth-issuer https://auth.example.com \
    --remote-auth-client-id my-client-id \
    --remote-auth-client-secret-file /path/to/secret

  # Proxy with OIDC protection for incoming requests
  thv proxy my-server --target-uri http://localhost:8080 \
    --oidc-issuer https://auth.example.com \
    --oidc-audience my-audience

  # Auto-detect authentication requirements
  thv proxy my-server --target-uri https://protected-api.com \
    --remote-auth-client-id my-client-id`,
	Args: cobra.ExactArgs(1),
	RunE: proxyCmdFunc,
}

var (
	proxyHost      string
	proxyPort      int
	proxyTargetURI string

	// Remote server authentication flags
	remoteAuthIssuer           string
	remoteAuthClientID         string
	remoteAuthClientSecret     string
	remoteAuthClientSecretFile string
	remoteAuthScopes           []string
	remoteAuthSkipBrowser      bool
	remoteAuthTimeout          time.Duration
	remoteAuthCallbackPort     int
	enableRemoteAuth           bool
)

// Default timeout constants
const (
	defaultOAuthTimeout      = 5 * time.Minute
	defaultHTTPTimeout       = 30 * time.Second
	defaultAuthDetectTimeout = 10 * time.Second
	maxRetryAttempts         = 3
	retryBaseDelay           = 2 * time.Second
)

// Environment variable names
const (
	// #nosec G101 - this is an environment variable name, not a credential
	envOAuthClientSecret = "TOOLHIVE_REMOTE_OAUTH_CLIENT_SECRET"
)

func init() {
	proxyCmd.Flags().StringVar(&proxyHost, "host", transport.LocalhostIPv4, "Host for the HTTP proxy to listen on (IP or hostname)")
	proxyCmd.Flags().IntVar(&proxyPort, "port", 0, "Port for the HTTP proxy to listen on (host port)")
	proxyCmd.Flags().StringVar(
		&proxyTargetURI,
		"target-uri",
		"",
		"URI for the target MCP server (e.g., http://localhost:8080) (required)",
	)

	// Add OIDC validation flags
	AddOIDCFlags(proxyCmd)

	// Add remote server authentication flags
	proxyCmd.Flags().BoolVar(&enableRemoteAuth, "remote-auth", false, "Enable OAuth authentication to remote MCP server")
	proxyCmd.Flags().StringVar(&remoteAuthIssuer, "remote-auth-issuer", "",
		"OAuth/OIDC issuer URL for remote server authentication (e.g., https://accounts.google.com)")
	proxyCmd.Flags().StringVar(&remoteAuthClientID, "remote-auth-client-id", "",
		"OAuth client ID for remote server authentication")
	proxyCmd.Flags().StringVar(&remoteAuthClientSecret, "remote-auth-client-secret", "",
		"OAuth client secret for remote server authentication (optional for PKCE)")
	proxyCmd.Flags().StringVar(&remoteAuthClientSecretFile, "remote-auth-client-secret-file", "",
		"Path to file containing OAuth client secret (alternative to --remote-auth-client-secret)")
	proxyCmd.Flags().StringSliceVar(&remoteAuthScopes, "remote-auth-scopes",
		[]string{"openid", "profile", "email"}, "OAuth scopes to request for remote server authentication")
	proxyCmd.Flags().BoolVar(&remoteAuthSkipBrowser, "remote-auth-skip-browser", false,
		"Skip opening browser for remote server OAuth flow")
	proxyCmd.Flags().DurationVar(&remoteAuthTimeout, "remote-auth-timeout", 30*time.Second,
		"Timeout for OAuth authentication flow (e.g., 30s, 1m, 2m30s)")
	proxyCmd.Flags().IntVar(&remoteAuthCallbackPort, "remote-auth-callback-port", 8666,
		"Port for OAuth callback server during remote authentication (default: 8666)")

	// Mark target-uri as required
	if err := proxyCmd.MarkFlagRequired("target-uri"); err != nil {
		logger.Warnf("Warning: Failed to mark flag as required: %v", err)
	}
}

func proxyCmdFunc(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// Get the server name
	serverName := args[0]

	// Validate the host flag and default resolving to IP in case hostname is provided
	validatedHost, err := ValidateAndNormaliseHostFlag(proxyHost)
	if err != nil {
		return fmt.Errorf("invalid host: %s", proxyHost)
	}
	proxyHost = validatedHost

	err = validateProxyTargetURI(proxyTargetURI)
	if err != nil {
		return fmt.Errorf("invalid target URI: %w", err)
	}

	// Select a port for the HTTP proxy (host port)
	port, err := networking.FindOrUsePort(proxyPort)
	if err != nil {
		return err
	}
	logger.Infof("Using host port: %d", port)

	// Handle OAuth authentication to the remote server if needed
	var tokenSource *oauth2.TokenSource

	if enableRemoteAuth || shouldDetectAuth() {
		tokenSource, err = handleOutgoingAuthentication(ctx)
		if err != nil {
			return fmt.Errorf("failed to authenticate to remote server: %w", err)
		}
	}

	// Create middlewares slice for incoming request authentication
	var middlewares []types.Middleware

	// Get OIDC configuration if enabled (for protecting the proxy endpoint)
	var oidcConfig *auth.TokenValidatorConfig
	if IsOIDCEnabled(cmd) {
		// Get OIDC flag values
		issuer := GetStringFlagOrEmpty(cmd, "oidc-issuer")
		audience := GetStringFlagOrEmpty(cmd, "oidc-audience")
		jwksURL := GetStringFlagOrEmpty(cmd, "oidc-jwks-url")
		clientID := GetStringFlagOrEmpty(cmd, "oidc-client-id")

		oidcConfig = &auth.TokenValidatorConfig{
			Issuer:   issuer,
			Audience: audience,
			JWKSURL:  jwksURL,
			ClientID: clientID,
		}
	}

	// Get authentication middleware for incoming requests
	authMiddleware, err := auth.GetAuthenticationMiddleware(ctx, oidcConfig, false)
	if err != nil {
		return fmt.Errorf("failed to create authentication middleware: %v", err)
	}
	middlewares = append(middlewares, authMiddleware)

	// Add OAuth token injection middleware for outgoing requests if we have an access token
	if tokenSource != nil {
		tokenMiddleware := createTokenInjectionMiddleware(tokenSource)
		middlewares = append(middlewares, tokenMiddleware)
	}

	// Create the transparent proxy
	logger.Infof("Setting up transparent proxy to forward from host port %d to %s",
		port, proxyTargetURI)

	// Create the transparent proxy with middlewares
	proxy := transparent.NewTransparentProxy(proxyHost, port, serverName, proxyTargetURI, nil, middlewares...)
	if err := proxy.Start(ctx); err != nil {
		return fmt.Errorf("failed to start proxy: %v", err)
	}

	logger.Infof("Transparent proxy started for server %s on port %d -> %s",
		serverName, port, proxyTargetURI)
	logger.Info("Press Ctrl+C to stop")

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigCh
	logger.Infof("Received signal %s, stopping proxy...", sig)

	// Stop the proxy
	if err := proxy.Stop(ctx); err != nil {
		logger.Warnf("Warning: Failed to stop proxy: %v", err)
	}

	logger.Infof("Proxy for server %s stopped", serverName)
	return nil
}

// AuthInfo contains authentication information extracted from WWW-Authenticate header
type AuthInfo struct {
	Realm string
	Type  string
}

// detectAuthenticationFromServer attempts to detect authentication requirements from the target server
func detectAuthenticationFromServer(ctx context.Context, targetURI string) (*AuthInfo, error) {
	// Create a context with timeout for auth detection
	detectCtx, cancel := context.WithTimeout(ctx, defaultAuthDetectTimeout)
	defer cancel()

	// Make a test request to the target server to see if it returns WWW-Authenticate
	client := &http.Client{
		Timeout: defaultAuthDetectTimeout,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   defaultHTTPTimeout / 3,
			ResponseHeaderTimeout: defaultHTTPTimeout / 3,
		},
	}

	req, err := http.NewRequestWithContext(detectCtx, http.MethodGet, targetURI, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Check if we got a 401 Unauthorized with WWW-Authenticate header
	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		if wwwAuth != "" {
			return parseWWWAuthenticate(wwwAuth)
		}
	}

	return nil, nil
}

// parseWWWAuthenticate parses the WWW-Authenticate header to extract realm and type
// Supports multiple authentication schemes and complex header formats
func parseWWWAuthenticate(header string) (*AuthInfo, error) {
	// Trim whitespace and handle empty headers
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, fmt.Errorf("empty WWW-Authenticate header")
	}

	// Split by comma to handle multiple authentication schemes
	schemes := strings.Split(header, ",")

	for _, scheme := range schemes {
		scheme = strings.TrimSpace(scheme)

		// Check for Bearer authentication
		if strings.HasPrefix(scheme, "Bearer") {
			authInfo := &AuthInfo{Type: "Bearer"}

			// Extract parameters after "Bearer"
			params := strings.TrimSpace(strings.TrimPrefix(scheme, "Bearer"))
			if params == "" {
				// Simple "Bearer" without parameters
				return authInfo, nil
			}

			// Parse parameters (realm, scope, etc.)
			realm := extractParameter(params, "realm")
			if realm != "" {
				authInfo.Realm = realm
			}

			return authInfo, nil
		}

		// Check for other authentication types (Basic, Digest, etc.)
		if strings.HasPrefix(scheme, "Basic") {
			return &AuthInfo{Type: "Basic"}, nil
		}

		if strings.HasPrefix(scheme, "Digest") {
			authInfo := &AuthInfo{Type: "Digest"}
			realm := extractParameter(scheme, "realm")
			if realm != "" {
				authInfo.Realm = realm
			}
			return authInfo, nil
		}
	}

	return nil, fmt.Errorf("no supported authentication type found in header: %s", header)
}

// extractParameter extracts a parameter value from an authentication header
func extractParameter(params, paramName string) string {
	// Look for paramName=value or paramName="value"
	parts := strings.Split(params, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, paramName+"=") {
			value := strings.TrimPrefix(part, paramName+"=")
			// Remove quotes if present
			value = strings.Trim(value, `"`)
			return value
		}
	}
	return ""
}

// performOAuthFlow performs the OAuth authentication flow
func performOAuthFlow(ctx context.Context, issuer, clientID, clientSecret string, scopes []string) (*oauth2.TokenSource, error) {
	logger.Info("Starting OAuth authentication flow...")

	// Create OAuth config from OIDC discovery
	oauthConfig, err := oauth.CreateOAuthConfigFromOIDC(
		ctx,
		issuer,
		clientID,
		clientSecret,
		scopes,
		true, // Enable PKCE by default for security
		remoteAuthCallbackPort,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth config: %w", err)
	}

	// Create OAuth flow
	flow, err := oauth.NewFlow(oauthConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth flow: %w", err)
	}

	// Create a context with timeout for the OAuth flow
	// Use the configured timeout, defaulting to the constant if not set
	oauthTimeout := remoteAuthTimeout
	if oauthTimeout <= 0 {
		oauthTimeout = defaultOAuthTimeout
	}

	oauthCtx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()

	// Start OAuth flow
	tokenResult, err := flow.Start(oauthCtx, remoteAuthSkipBrowser)
	if err != nil {
		if oauthCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("OAuth flow timed out after %v - user did not complete authentication", oauthTimeout)
		}
		return nil, fmt.Errorf("OAuth flow failed: %w", err)
	}

	logger.Info("OAuth authentication successful")

	// Log token info (without exposing the actual token)
	if tokenResult.Claims != nil {
		if sub, ok := tokenResult.Claims["sub"].(string); ok {
			logger.Infof("Authenticated as subject: %s", sub)
		}
		if email, ok := tokenResult.Claims["email"].(string); ok {
			logger.Infof("Authenticated email: %s", email)
		}
	}

	source := flow.TokenSource()
	return &source, nil
}

// shouldDetectAuth determines if we should try to detect authentication requirements
func shouldDetectAuth() bool {
	// Only try to detect auth if OAuth client ID is provided
	// This prevents unnecessary requests when no OAuth config is available
	return remoteAuthClientID != ""
}

// handleOutgoingAuthentication handles authentication to the remote MCP server
func handleOutgoingAuthentication(ctx context.Context) (*oauth2.TokenSource, error) {
	// Resolve client secret from multiple sources
	clientSecret, err := resolveClientSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve client secret: %w", err)
	}

	if enableRemoteAuth {
		// If OAuth is explicitly enabled, use provided configuration
		if remoteAuthIssuer == "" {
			return nil, fmt.Errorf("remote-auth-issuer is required when remote authentication is enabled")
		}
		if remoteAuthClientID == "" {
			return nil, fmt.Errorf("remote-auth-client-id is required when remote authentication is enabled")
		}

		return performOAuthFlow(ctx, remoteAuthIssuer, remoteAuthClientID, clientSecret, remoteAuthScopes)
	}

	// Try to detect authentication requirements from WWW-Authenticate header
	authInfo, err := detectAuthenticationFromServer(ctx, proxyTargetURI)
	if err != nil {
		logger.Debugf("Could not detect authentication from server: %v", err)
		return nil, nil // Not an error, just no auth detected
	}

	if authInfo != nil {
		logger.Infof("Detected authentication requirement from server: %s", authInfo.Realm)

		if remoteAuthClientID == "" {
			return nil, fmt.Errorf("detected OAuth requirement but no remote-auth-client-id provided")
		}

		// Perform OAuth flow with discovered configuration
		return performOAuthFlow(ctx, authInfo.Realm, remoteAuthClientID, clientSecret, remoteAuthScopes)
	}

	return nil, nil // No authentication required
}

// resolveClientSecret resolves the OAuth client secret from multiple sources
// Priority: 1. Flag value, 2. File, 3. Environment variable
func resolveClientSecret() (string, error) {
	// 1. Check if provided directly via flag
	if remoteAuthClientSecret != "" {
		logger.Debug("Using client secret from command-line flag")
		return remoteAuthClientSecret, nil
	}

	// 2. Check if provided via file
	if remoteAuthClientSecretFile != "" {
		// Clean the file path to prevent path traversal
		cleanPath := filepath.Clean(remoteAuthClientSecretFile)
		logger.Debugf("Reading client secret from file: %s", cleanPath)
		// #nosec G304 - file path is cleaned above
		secretBytes, err := os.ReadFile(cleanPath)
		if err != nil {
			return "", fmt.Errorf("failed to read client secret file %s: %w", cleanPath, err)
		}
		secret := strings.TrimSpace(string(secretBytes))
		if secret == "" {
			return "", fmt.Errorf("client secret file %s is empty", cleanPath)
		}
		return secret, nil
	}

	// 3. Check environment variable
	if secret := os.Getenv(envOAuthClientSecret); secret != "" {
		logger.Debugf("Using client secret from %s environment variable", envOAuthClientSecret)
		return secret, nil
	}

	// No client secret found - this is acceptable for PKCE flows
	logger.Debug("No client secret provided - using PKCE flow")
	return "", nil
}

// createTokenInjectionMiddleware creates a middleware that injects the OAuth token into requests
func createTokenInjectionMiddleware(tokenSource *oauth2.TokenSource) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := (*tokenSource).Token()
			if err != nil {
				http.Error(w, "Unable to retrieve OAuth token", http.StatusUnauthorized)
				return
			}

			r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
			next.ServeHTTP(w, r)
		})
	}
}

// validateProxyTargetURI validates that the target URI for the proxy is valid and does not contain a path
func validateProxyTargetURI(targetURI string) error {
	// Parse the target URI
	targetURL, err := url.Parse(targetURI)
	if err != nil {
		return fmt.Errorf("invalid target URI: %w", err)
	}

	// Check if the path is empty or just "/"
	if targetURL.Path != "" && targetURL.Path != "/" {
		return fmt.Errorf("target URI should not contain a path, got: %s", proxyTargetURI)
	}

	return nil
}

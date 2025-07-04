// Package auth provides authentication and authorization utilities.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"

	"github.com/stacklok/toolhive/pkg/kubernetes/versions"
)

// Common errors
var (
	ErrNoToken                 = errors.New("no token provided")
	ErrInvalidToken            = errors.New("invalid token")
	ErrTokenExpired            = errors.New("token expired")
	ErrInvalidIssuer           = errors.New("invalid issuer")
	ErrInvalidAudience         = errors.New("invalid audience")
	ErrMissingJWKSURL          = errors.New("missing JWKS URL")
	ErrFailedToFetchJWKS       = errors.New("failed to fetch JWKS")
	ErrFailedToDiscoverOIDC    = errors.New("failed to discover OIDC configuration")
	ErrMissingIssuerAndJWKSURL = errors.New("either issuer or JWKS URL must be provided")
)

// OIDCDiscoveryDocument represents the OIDC discovery document structure
type OIDCDiscoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	// Add other fields as needed
}

// TokenValidator validates JWT or opaque tokens using OIDC configuration.
type TokenValidator struct {
	// OIDC configuration
	issuer            string
	audience          string
	jwksURL           string
	clientID          string
	jwksClient        *jwk.Cache
	allowOpaqueTokens bool // Whether to allow opaque tokens (non-JWT)

	// No need for additional caching as jwk.Cache handles it
}

// TokenValidatorConfig contains configuration for the token validator.
type TokenValidatorConfig struct {
	// Issuer is the OIDC issuer URL (e.g., https://accounts.google.com)
	Issuer string

	// Audience is the expected audience for the token
	Audience string

	// JWKSURL is the URL to fetch the JWKS from
	JWKSURL string

	// ClientID is the OIDC client ID
	ClientID string

	// AllowOpaqueTokens indicates whether to allow opaque tokens (non-JWT)
	AllowOpaqueTokens bool
}

// discoverOIDCConfiguration discovers OIDC configuration from the issuer's well-known endpoint
func discoverOIDCConfiguration(ctx context.Context, issuer string) (*OIDCDiscoveryDocument, error) {
	// Construct the well-known endpoint URL
	wellKnownURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set User-Agent header
	req.Header.Set("User-Agent", fmt.Sprintf("ToolHive/%s", versions.Version))
	req.Header.Set("Accept", "application/json")

	// Make the request
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OIDC configuration: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery endpoint returned status %d", resp.StatusCode)
	}

	// Parse the response
	var doc OIDCDiscoveryDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC configuration: %w", err)
	}

	// Validate that we got the required fields
	if doc.JWKSURI == "" {
		return nil, fmt.Errorf("OIDC configuration missing jwks_uri")
	}

	return &doc, nil
}

// NewTokenValidatorConfig creates a new TokenValidatorConfig with the provided parameters
func NewTokenValidatorConfig(issuer, audience, jwksURL, clientID string, allowOpaqueTokens bool) *TokenValidatorConfig {
	// Only create a config if at least one parameter is provided
	if issuer == "" && audience == "" && jwksURL == "" && clientID == "" {
		return nil
	}

	return &TokenValidatorConfig{
		Issuer:            issuer,
		Audience:          audience,
		JWKSURL:           jwksURL,
		ClientID:          clientID,
		AllowOpaqueTokens: allowOpaqueTokens,
	}
}

// NewTokenValidator creates a new token validator.
func NewTokenValidator(ctx context.Context, config TokenValidatorConfig, allowOpaqueTokens bool) (*TokenValidator, error) {
	jwksURL := config.JWKSURL

	// If JWKS URL is not provided but issuer is, try to discover it
	if jwksURL == "" && config.Issuer != "" {
		doc, err := discoverOIDCConfiguration(ctx, config.Issuer)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrFailedToDiscoverOIDC, err)
		}
		jwksURL = doc.JWKSURI
	}

	// Ensure we have a JWKS URL either provided or discovered
	if jwksURL == "" {
		return nil, ErrMissingIssuerAndJWKSURL
	}

	// Create a new JWKS client with auto-refresh
	cache := jwk.NewCache(ctx)

	// Register the JWKS URL with the cache
	err := cache.Register(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to register JWKS URL: %w", err)
	}

	return &TokenValidator{
		issuer:            config.Issuer,
		audience:          config.Audience,
		jwksURL:           jwksURL,
		clientID:          config.ClientID,
		jwksClient:        cache,
		allowOpaqueTokens: allowOpaqueTokens,
	}, nil
}

// getKeyFromJWKS gets the key from the JWKS.
func (v *TokenValidator) getKeyFromJWKS(ctx context.Context, token *jwt.Token) (interface{}, error) {
	// Validate the signing method
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	}

	// Get the key ID from the token header
	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, fmt.Errorf("token header missing kid")
	}

	// Get the key set from the JWKS
	keySet, err := v.jwksClient.Get(ctx, v.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get JWKS: %w", err)
	}

	// Get the key with the matching key ID
	key, found := keySet.LookupKeyID(kid)
	if !found {
		return nil, fmt.Errorf("key ID %s not found in JWKS", kid)
	}

	// Get the raw key
	var rawKey interface{}
	if err := key.Raw(&rawKey); err != nil {
		return nil, fmt.Errorf("failed to get raw key: %w", err)
	}

	return rawKey, nil
}

// validateClaims validates the claims in the token.
func (v *TokenValidator) validateClaims(claims jwt.MapClaims) error {
	// Validate the issuer if provided
	if v.issuer != "" {
		issuer, err := claims.GetIssuer()
		if err != nil || issuer != v.issuer {
			return ErrInvalidIssuer
		}
	}

	// Validate the audience if provided
	if v.audience != "" {
		audiences, err := claims.GetAudience()
		if err != nil {
			return ErrInvalidAudience
		}

		found := false
		for _, aud := range audiences {
			if aud == v.audience {
				found = true
				break
			}
		}

		if !found {
			return ErrInvalidAudience
		}
	}

	// Validate the expiration time
	expirationTime, err := claims.GetExpirationTime()
	if err != nil || expirationTime == nil || expirationTime.Before(time.Now()) {
		return ErrTokenExpired
	}

	return nil
}

// ValidateToken validates a token.
func (v *TokenValidator) ValidateToken(ctx context.Context, tokenString string) (jwt.MapClaims, error) {
	// Parse the token
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		return v.getKeyFromJWKS(ctx, token)
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenMalformed) {
			// just an opaque token, let it run
			if v.allowOpaqueTokens {
				return nil, nil
			}
			return nil, fmt.Errorf("token seems to be opaque, but opaque tokens are not allowed: %w", err)
		}
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	// it is a jwt token
	// Check if the token is valid
	if !token.Valid {
		return nil, ErrInvalidToken
	}

	// Get the claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("failed to get claims from token")
	}

	// Validate the claims
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// ClaimsContextKey is the key used to store claims in the request context.
type ClaimsContextKey struct{}

// Middleware creates an HTTP middleware that validates JWT tokens.
func (v *TokenValidator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get the token from the Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=\"%s\"", v.issuer))
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		// Check if the Authorization header has the Bearer prefix
		if !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=\"%s\"", v.issuer))
			http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
			return
		}

		// Extract the token
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// Validate the token
		claims, err := v.ValidateToken(r.Context(), tokenString)
		if err != nil {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(
				"Bearer realm=\"%s\", error=\"invalid_token\", error_description=\"%v\"",
				v.issuer, err,
			))
			http.Error(w, fmt.Sprintf("Invalid token: %v", err), http.StatusUnauthorized)
			return
		}

		// Add the claims to the request context using a proper key type
		ctx := context.WithValue(r.Context(), ClaimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

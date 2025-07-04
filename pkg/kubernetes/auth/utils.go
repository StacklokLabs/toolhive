// Package auth provides authentication and authorization utilities.
package auth

import (
	"context"
	"net/http"
	"os/user"

	"github.com/golang-jwt/jwt/v5"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// GetClaimsFromContext retrieves the claims from the request context.
// This is a helper function that can be used by authorization policies
// to access the claims regardless of which middleware was used (JWT, anonymous, or local).
//
// Returns the claims and a boolean indicating whether claims were found.
func GetClaimsFromContext(ctx context.Context) (jwt.MapClaims, bool) {
	if ctx == nil {
		return nil, false
	}
	claims, ok := ctx.Value(ClaimsContextKey{}).(jwt.MapClaims)
	return claims, ok
}

// GetAuthenticationMiddleware returns the appropriate authentication middleware based on the configuration.
// If OIDC config is provided, it returns JWT middleware. Otherwise, it returns local user middleware.
func GetAuthenticationMiddleware(ctx context.Context, oidcConfig *TokenValidatorConfig,
	allowOpaqueTokens bool) (func(http.Handler) http.Handler, error) {
	if oidcConfig != nil {
		logger.Info("OIDC validation enabled")

		// Create JWT validator
		jwtValidator, err := NewTokenValidator(ctx, *oidcConfig, allowOpaqueTokens)
		if err != nil {
			return nil, err
		}

		return jwtValidator.Middleware, nil
	}

	logger.Info("OIDC validation disabled, using local user authentication")

	// Get current OS user
	currentUser, err := user.Current()
	if err != nil {
		logger.Warnf("Failed to get current user, using 'local' as default: %v", err)
		return LocalUserMiddleware("local"), nil
	}

	logger.Infof("Using local user authentication for user: %s", currentUser.Username)
	return LocalUserMiddleware(currentUser.Username), nil
}

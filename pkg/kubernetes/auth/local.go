// Package auth provides authentication and authorization utilities.
package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// LocalUserMiddleware creates an HTTP middleware that sets up local user claims.
// This allows specifying a local username while still bypassing authentication.
//
// This middleware is useful for development and testing scenarios where you want
// to simulate a specific user without going through the full authentication flow.
// Like AnonymousMiddleware, this is heavily discouraged in production settings.
func LocalUserMiddleware(username string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create local user claims with the specified username
			claims := jwt.MapClaims{
				"sub":   username,
				"iss":   "toolhive-local",
				"aud":   "toolhive",
				"exp":   time.Now().Add(24 * time.Hour).Unix(), // Valid for 24 hours
				"iat":   time.Now().Unix(),
				"nbf":   time.Now().Unix(),
				"email": username + "@localhost",
				"name":  "Local User: " + username,
			}

			// Add the local user claims to the request context using the same key
			// as the JWT middleware for consistency
			ctx := context.WithValue(r.Context(), ClaimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

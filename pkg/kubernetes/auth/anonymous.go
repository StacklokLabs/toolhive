// Package auth provides authentication and authorization utilities.
package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AnonymousMiddleware creates an HTTP middleware that sets up anonymous claims.
// This is useful for testing and local environments where authorization policies
// need to work without requiring actual authentication.
//
// The middleware sets up basic anonymous claims that can be used by authorization
// policies, allowing them to function even when authentication is disabled.
// This is heavily discouraged in production settings but is handy for testing
// and local development environments.
func AnonymousMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create anonymous claims with basic information
		claims := jwt.MapClaims{
			"sub":   "anonymous",
			"iss":   "toolhive-local",
			"aud":   "toolhive",
			"exp":   time.Now().Add(24 * time.Hour).Unix(), // Valid for 24 hours
			"iat":   time.Now().Unix(),
			"nbf":   time.Now().Unix(),
			"email": "anonymous@localhost",
			"name":  "Anonymous User",
		}

		// Add the anonymous claims to the request context using the same key
		// as the JWT middleware for consistency
		ctx := context.WithValue(r.Context(), ClaimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

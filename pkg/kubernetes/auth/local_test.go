package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalUserMiddleware(t *testing.T) {
	t.Parallel()
	username := "testuser"

	// Create a test handler that checks for claims in the context
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := GetClaimsFromContext(r.Context())
		require.True(t, ok, "Expected claims to be present in context")

		// Verify the local user claims
		assert.Equal(t, username, claims["sub"])
		assert.Equal(t, "toolhive-local", claims["iss"])
		assert.Equal(t, "toolhive", claims["aud"])
		assert.Equal(t, username+"@localhost", claims["email"])
		assert.Equal(t, "Local User: "+username, claims["name"])

		// Verify timestamps are reasonable
		now := time.Now().Unix()
		exp, ok := claims["exp"].(int64)
		require.True(t, ok, "Expected exp to be present and be an int64")
		assert.Greater(t, exp, now, "Expected exp to be in the future")

		iat, ok := claims["iat"].(int64)
		require.True(t, ok, "Expected iat to be present and be an int64")
		assert.LessOrEqual(t, iat, now+1, "Expected iat to be current time or earlier (with 1 second tolerance)")

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap the test handler with the local user middleware
	middleware := LocalUserMiddleware(username)(testHandler)

	// Create a test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Execute the request
	middleware.ServeHTTP(w, req)

	// Check the response
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestLocalUserMiddlewareWithDifferentUsernames(t *testing.T) {
	t.Parallel()
	testCases := []string{"alice", "bob", "admin", "user123"}

	for _, username := range testCases {
		t.Run("username_"+username, func(t *testing.T) {
			t.Parallel()
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := GetClaimsFromContext(r.Context())
				require.True(t, ok, "Expected claims to be present in context")

				assert.Equal(t, username, claims["sub"])
				assert.Equal(t, username+"@localhost", claims["email"])

				w.WriteHeader(http.StatusOK)
			})

			middleware := LocalUserMiddleware(username)(testHandler)
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

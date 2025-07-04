package authz

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// TestNewCedarAuthorizer tests the creation of a new Cedar authorizer with different configurations.
func TestNewCedarAuthorizer(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()
	// Test cases
	testCases := []struct {
		name         string
		policies     []string
		entitiesJSON string
		expectError  bool
		errorType    error
	}{
		{
			name:         "Valid policy and empty entities",
			policies:     []string{`permit(principal, action, resource);`},
			entitiesJSON: `[]`,
			expectError:  false,
		},
		{
			name:         "Multiple valid policies",
			policies:     []string{`permit(principal, action, resource);`, `forbid(principal, action, resource);`},
			entitiesJSON: `[]`,
			expectError:  false,
		},
		{
			name:         "Invalid policy",
			policies:     []string{`invalid policy syntax`},
			entitiesJSON: `[]`,
			expectError:  true,
		},
		{
			name:         "No policies",
			policies:     []string{},
			entitiesJSON: `[]`,
			expectError:  true,
			errorType:    ErrNoPolicies,
		},
		{
			name:         "Invalid entities JSON",
			policies:     []string{`permit(principal, action, resource);`},
			entitiesJSON: `invalid json`,
			expectError:  true,
		},
		{
			name:         "Valid policy and valid entities",
			policies:     []string{`permit(principal, action, resource);`},
			entitiesJSON: `[{"uid": {"type": "User", "id": "alice"}, "attrs": {}, "parents": []}]`,
			expectError:  false,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a Cedar authorizer
			authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
				Policies:     tc.policies,
				EntitiesJSON: tc.entitiesJSON,
			})

			// Check error expectations
			if tc.expectError {
				assert.Error(t, err, "Expected an error but got none")
				if tc.errorType != nil {
					assert.ErrorIs(t, err, tc.errorType, "Expected error type %v but got %v", tc.errorType, err)
				}
				assert.Nil(t, authorizer, "Expected nil authorizer when error occurs")
			} else {
				assert.NoError(t, err, "Unexpected error: %v", err)
				require.NotNil(t, authorizer, "Cedar authorizer is nil")
				assert.NotNil(t, authorizer.policySet, "Cedar policy set is nil")
				assert.NotNil(t, authorizer.entities, "Cedar entities map is nil")
			}
		})
	}
}

// TestAuthorizeWithJWTClaims tests the AuthorizeWithJWTClaims function with different roles in claims.
func TestAuthorizeWithJWTClaims(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name             string
		policy           string
		claims           jwt.MapClaims
		feature          MCPFeature
		operation        MCPOperation
		resourceID       string
		arguments        map[string]interface{}
		expectAuthorized bool
	}{
		{
			name: "User with correct name can call weather tool",
			policy: `
			permit(
				principal,
				action == Action::"call_tool",
				resource == Tool::"weather"
			)
			when {
				context.claim_name == "John Doe"
			};
			`,
			claims: jwt.MapClaims{
				"sub":   "user123",
				"name":  "John Doe",
				"roles": []string{"user", "reader"},
			},
			feature:          MCPFeatureTool,
			operation:        MCPOperationCall,
			resourceID:       "weather",
			arguments:        nil,
			expectAuthorized: true,
		},
		{
			name: "User with incorrect name cannot call weather tool",
			policy: `
			permit(
				principal,
				action == Action::"call_tool",
				resource == Tool::"weather"
			)
			when {
				context.claim_name == "John Doe"
			};
			`,
			claims: jwt.MapClaims{
				"sub":   "user123",
				"name":  "Jane Smith",
				"roles": []string{"user", "reader"},
			},
			feature:          MCPFeatureTool,
			operation:        MCPOperationCall,
			resourceID:       "weather",
			arguments:        nil,
			expectAuthorized: false,
		},
		{
			name: "Admin user can call any tool",
			policy: `
			permit(
				principal,
				action == Action::"call_tool",
				resource
			)
			when {
				context.claim_role == "admin"
			};
			`,
			claims: map[string]interface{}{
				"sub":  "admin123",
				"name": "Admin User",
				"role": "admin",
			},
			feature:          MCPFeatureTool,
			operation:        MCPOperationCall,
			resourceID:       "any_tool",
			arguments:        nil,
			expectAuthorized: true,
		},
		{
			name: "User with specific argument value can call tool",
			policy: `
			permit(
				principal,
				action == Action::"call_tool",
				resource == Tool::"calculator"
			)
			when {
				context.arg_operation == "add" && context.arg_value1 == 5
			};
			`,
			claims: map[string]interface{}{
				"sub":  "user123",
				"name": "John Doe",
			},
			feature:    MCPFeatureTool,
			operation:  MCPOperationCall,
			resourceID: "calculator",
			arguments: map[string]interface{}{
				"operation": "add",
				"value1":    5,
				"value2":    10,
			},
			expectAuthorized: true,
		},
		{
			name: "User with specific role in array can access resource",
			policy: `
			permit(
				principal,
				action == Action::"read_resource",
				resource == Resource::"sensitive_data"
			)
			when {
				context.claim_groups.contains("editor")
			};
			`,
			claims: jwt.MapClaims{
				"sub":    "user123",
				"name":   "John Doe",
				"groups": []string{"reader", "editor", "viewer"},
			},
			feature:          MCPFeatureResource,
			operation:        MCPOperationRead,
			resourceID:       "sensitive_data",
			arguments:        nil,
			expectAuthorized: true,
		},
		{
			name: "User can get prompt",
			policy: `
			permit(
				principal,
				action == Action::"get_prompt",
				resource == Prompt::"greeting"
			);
			`,
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
				"role": "user",
			},
			feature:          MCPFeaturePrompt,
			operation:        MCPOperationGet,
			resourceID:       "greeting",
			arguments:        nil,
			expectAuthorized: true,
		},
		{
			name: "User can list tools",
			policy: `
			permit(
				principal,
				action == Action::"list_tools",
				resource == FeatureType::"tool"
			);
			`,
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
				"role": "user",
			},
			feature:          MCPFeatureTool,
			operation:        MCPOperationList,
			resourceID:       "",
			arguments:        nil,
			expectAuthorized: true,
		},
		{
			name: "User can list prompts",
			policy: `
			permit(
				principal,
				action == Action::"list_prompts",
				resource == FeatureType::"prompt"
			);
			`,
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
				"role": "user",
			},
			feature:          MCPFeaturePrompt,
			operation:        MCPOperationList,
			resourceID:       "",
			arguments:        nil,
			expectAuthorized: true,
		},
		{
			name: "User can list resources",
			policy: `
			permit(
				principal,
				action == Action::"list_resources",
				resource == FeatureType::"resource"
			);
			`,
			claims: jwt.MapClaims{
				"sub":  "user123",
				"name": "John Doe",
				"role": "user",
			},
			feature:          MCPFeatureResource,
			operation:        MCPOperationList,
			resourceID:       "",
			arguments:        nil,
			expectAuthorized: true,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a context
			ctx := context.Background()

			// Create a Cedar authorizer
			authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
				Policies:     []string{tc.policy},
				EntitiesJSON: `[]`,
			})
			require.NoError(t, err, "Failed to create Cedar authorizer")

			// Create a context with JWT claims
			claimsCtx := context.WithValue(ctx, auth.ClaimsContextKey{}, tc.claims)

			// Test authorization
			authorized, err := authorizer.AuthorizeWithJWTClaims(claimsCtx, tc.feature, tc.operation, tc.resourceID, tc.arguments)
			assert.NoError(t, err, "Authorization error")
			assert.Equal(t, tc.expectAuthorized, authorized, "Authorization result does not match expectation")
		})
	}
}

// TestAuthorizeWithJWTClaimsErrors tests error cases for AuthorizeWithJWTClaims.
func TestAuthorizeWithJWTClaimsErrors(t *testing.T) {
	t.Parallel()
	// Create a context
	ctx := context.Background()

	// Create a Cedar authorizer
	authorizer, err := NewCedarAuthorizer(CedarAuthorizerConfig{
		Policies:     []string{`permit(principal, action, resource);`},
		EntitiesJSON: `[]`,
	})
	require.NoError(t, err, "Failed to create Cedar authorizer")

	// Test cases
	testCases := []struct {
		name        string
		setupCtx    func(context.Context) context.Context
		feature     MCPFeature
		operation   MCPOperation
		resourceID  string
		arguments   map[string]interface{}
		expectError bool
		errorType   error
	}{
		{
			name: "Missing claims in context",
			setupCtx: func(ctx context.Context) context.Context {
				// Don't add claims to the context
				return ctx
			},
			feature:     MCPFeatureTool,
			operation:   MCPOperationCall,
			resourceID:  "weather",
			arguments:   nil,
			expectError: true,
			errorType:   ErrMissingPrincipal,
		},
		{
			name: "Missing sub claim",
			setupCtx: func(ctx context.Context) context.Context {
				// Add claims without sub
				claims := jwt.MapClaims{
					"name": "John Doe",
					"role": "user",
				}
				return context.WithValue(ctx, auth.ClaimsContextKey{}, claims)
			},
			feature:     MCPFeatureTool,
			operation:   MCPOperationCall,
			resourceID:  "weather",
			arguments:   nil,
			expectError: true,
			errorType:   ErrMissingPrincipal,
		},
		{
			name: "Empty sub claim",
			setupCtx: func(ctx context.Context) context.Context {
				// Add claims with empty sub
				claims := jwt.MapClaims{
					"sub":  "",
					"name": "John Doe",
					"role": "user",
				}
				return context.WithValue(ctx, auth.ClaimsContextKey{}, claims)
			},
			feature:     MCPFeatureTool,
			operation:   MCPOperationCall,
			resourceID:  "weather",
			arguments:   nil,
			expectError: true,
			errorType:   ErrMissingPrincipal,
		},
		{
			name: "Unsupported feature/operation combination",
			setupCtx: func(ctx context.Context) context.Context {
				// Add valid claims
				claims := jwt.MapClaims{
					"sub":  "user123",
					"name": "John Doe",
					"role": "user",
				}
				return context.WithValue(ctx, auth.ClaimsContextKey{}, claims)
			},
			feature:     "invalid_feature",
			operation:   "invalid_operation",
			resourceID:  "resource",
			arguments:   nil,
			expectError: true,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Setup context
			testCtx := tc.setupCtx(ctx)

			// Test authorization
			_, err := authorizer.AuthorizeWithJWTClaims(testCtx, tc.feature, tc.operation, tc.resourceID, tc.arguments)
			assert.Error(t, err, "Expected an error")
			if tc.errorType != nil {
				assert.ErrorIs(t, err, tc.errorType, "Expected error type %v but got %v", tc.errorType, err)
			}
		})
	}
}

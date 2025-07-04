package authz

import (
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateCedarEntities tests the createCedarEntities function.
func TestCreateCedarEntities(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name       string
		principal  string
		action     string
		resource   string
		claimsMap  map[string]interface{}
		attributes map[string]interface{}
		expectErr  bool
	}{
		{
			name:      "Valid entities",
			principal: "Client::user123",
			action:    "Action::call_tool",
			resource:  "Tool::weather",
			claimsMap: map[string]interface{}{
				"claim_sub":   "user123",
				"claim_name":  "John Doe",
				"claim_roles": []string{"user", "admin"},
			},
			attributes: map[string]interface{}{
				"name":      "weather",
				"operation": "call",
				"feature":   "tool",
			},
			expectErr: false,
		},
		{
			name:       "Invalid principal format",
			principal:  "user123",
			action:     "Action::call_tool",
			resource:   "Tool::weather",
			claimsMap:  map[string]interface{}{},
			attributes: map[string]interface{}{},
			expectErr:  true,
		},
		{
			name:       "Invalid action format",
			principal:  "Client::user123",
			action:     "call_tool",
			resource:   "Tool::weather",
			claimsMap:  map[string]interface{}{},
			attributes: map[string]interface{}{},
			expectErr:  true,
		},
		{
			name:       "Invalid resource format",
			principal:  "Client::user123",
			action:     "Action::call_tool",
			resource:   "weather",
			claimsMap:  map[string]interface{}{},
			attributes: map[string]interface{}{},
			expectErr:  true,
		},
		{
			name:      "With complex attributes",
			principal: "Client::user123",
			action:    "Action::call_tool",
			resource:  "Tool::calculator",
			claimsMap: map[string]interface{}{
				"claim_sub":             "user123",
				"claim_name":            "John Doe",
				"claim_roles":           []string{"user", "admin"},
				"claim_clearance_level": 5,
			},
			attributes: map[string]interface{}{
				"name":          "calculator",
				"operation":     "call",
				"feature":       "tool",
				"arg_operation": "add",
				"arg_value1":    5,
				"arg_value2":    10,
				"tags":          []string{"math", "utility"},
				"priority":      1,
				"enabled":       true,
			},
			expectErr: false,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create entity factory
			factory := NewEntityFactory()

			// Create Cedar entities
			entities, err := factory.CreateEntitiesForRequest(tc.principal, tc.action, tc.resource, tc.claimsMap, tc.attributes)

			// Check error expectations
			if tc.expectErr {
				assert.Error(t, err, "Expected an error but got none")
				assert.Nil(t, entities, "Expected nil entities when error occurs")
				return
			}

			assert.NoError(t, err, "Unexpected error: %v", err)
			assert.NotNil(t, entities, "Entities should not be nil")

			// Check that we have three entities (principal, action, resource)
			assert.Len(t, entities, 3, "Expected three entities")

			// Basic validation of entity structure
			for _, entity := range entities {
				// Each entity should have UID, Attributes, and Parents fields
				assert.NotNil(t, entity.UID, "Entity should have a UID field")
				assert.NotNil(t, entity.Attributes, "Entity should have an Attributes field")
				assert.NotNil(t, entity.Parents, "Entity should have a Parents field")
			}

			// Verify that the principal entity has the correct type and ID
			if !tc.expectErr {
				principalType, principalID, err := parseCedarEntityID(tc.principal)
				require.NoError(t, err, "Failed to parse principal ID")
				principalUID := cedar.NewEntityUID(cedar.EntityType(principalType), cedar.String(principalID))
				principalEntity, found := entities[principalUID]
				assert.True(t, found, "Principal entity not found")
				assert.Equal(t, principalType, string(principalEntity.UID.Type), "Principal type does not match")
				assert.Equal(t, principalID, string(principalEntity.UID.ID), "Principal ID does not match")

				// Verify that the action entity has the correct type and ID
				actionType, actionID, err := parseCedarEntityID(tc.action)
				require.NoError(t, err, "Failed to parse action ID")
				actionUID := cedar.NewEntityUID(cedar.EntityType(actionType), cedar.String(actionID))
				actionEntity, found := entities[actionUID]
				assert.True(t, found, "Action entity not found")
				assert.Equal(t, actionType, string(actionEntity.UID.Type), "Action type does not match")
				assert.Equal(t, actionID, string(actionEntity.UID.ID), "Action ID does not match")

				// Verify that the resource entity has the correct type and ID
				resourceType, resourceID, err := parseCedarEntityID(tc.resource)
				require.NoError(t, err, "Failed to parse resource ID")
				resourceUID := cedar.NewEntityUID(cedar.EntityType(resourceType), cedar.String(resourceID))
				resourceEntity, found := entities[resourceUID]
				assert.True(t, found, "Resource entity not found")
				assert.Equal(t, resourceType, string(resourceEntity.UID.Type), "Resource type does not match")
				assert.Equal(t, resourceID, string(resourceEntity.UID.ID), "Resource ID does not match")

				// Verify that the action entity has the operation attribute
				operationValue, found := actionEntity.Attributes.Get(cedar.String("operation"))
				assert.True(t, found, "Operation attribute not found")
				assert.Equal(t, actionID, string(operationValue.(cedar.String)), "Action operation attribute does not match")

				// Verify that the resource entity has the name attribute
				_, found = resourceEntity.Attributes.Get(cedar.String("name"))
				assert.True(t, found, "Resource name attribute not found")

				// Verify that JWT claims are added to the principal entity
				if len(tc.claimsMap) > 0 {
					for k, v := range tc.claimsMap {
						claimValue, found := principalEntity.Attributes.Get(cedar.String(k))
						assert.True(t, found, "Claim %s not found in principal entity", k)

						// For string claims, we can directly compare the values
						if strVal, ok := v.(string); ok {
							assert.Equal(t, strVal, string(claimValue.(cedar.String)), "Claim %s value does not match", k)
						}
					}
				}
			}
		})
	}
}

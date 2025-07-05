// Package authz provides authorization utilities using Cedar policies.
package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/golang-jwt/jwt/v5"

	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// Common errors for Cedar authorization
var (
	ErrNoPolicies           = errors.New("no policies loaded")
	ErrInvalidPolicy        = errors.New("invalid policy")
	ErrUnauthorized         = errors.New("unauthorized")
	ErrMissingPrincipal     = errors.New("missing principal")
	ErrMissingAction        = errors.New("missing action")
	ErrMissingResource      = errors.New("missing resource")
	ErrFailedToLoadEntities = errors.New("failed to load entities")
)

// ClientIDContextKey is the key used to store client ID in the context.
type ClientIDContextKey struct{}

// MCPFeature represents an MCP feature type.
// In the MCP protocol, there are three main features:
// - Tools: Allow models to call functions in external systems
// - Prompts: Provide structured templates for interacting with language models
// - Resources: Share data that provides context to language models
type MCPFeature string

const (
	// MCPFeatureTool represents the MCP tool feature.
	MCPFeatureTool MCPFeature = "tool"
	// MCPFeaturePrompt represents the MCP prompt feature.
	MCPFeaturePrompt MCPFeature = "prompt"
	// MCPFeatureResource represents the MCP resource feature.
	MCPFeatureResource MCPFeature = "resource"
)

// MCPOperation represents an operation on an MCP feature.
// Each feature supports different operations:
// - List: Get a list of available items (tools, prompts, resources)
// - Get: Get a specific prompt
// - Call: Call a specific tool
// - Read: Read a specific resource
type MCPOperation string

const (
	// MCPOperationList represents a list operation.
	MCPOperationList MCPOperation = "list"
	// MCPOperationGet represents a get operation.
	MCPOperationGet MCPOperation = "get"
	// MCPOperationCall represents a call operation.
	MCPOperationCall MCPOperation = "call"
	// MCPOperationRead represents a read operation.
	MCPOperationRead MCPOperation = "read"
)

// CedarAuthorizer authorizes MCP operations using Cedar policies.
type CedarAuthorizer struct {
	// Cedar policy set
	policySet *cedar.PolicySet
	// Cedar entities
	entities cedar.EntityMap
	// Entity factory for creating entities
	entityFactory *EntityFactory
	// Mutex for thread safety
	mu sync.RWMutex
}

// CedarAuthorizerConfig contains configuration for the Cedar authorizer.
type CedarAuthorizerConfig struct {
	// Policies is a list of Cedar policy strings
	Policies []string
	// EntitiesJSON is the JSON string representing Cedar entities
	EntitiesJSON string
}

// NewCedarAuthorizer creates a new Cedar authorizer.
func NewCedarAuthorizer(config CedarAuthorizerConfig) (*CedarAuthorizer, error) {
	authorizer := &CedarAuthorizer{
		policySet:     cedar.NewPolicySet(),
		entities:      cedar.EntityMap{},
		entityFactory: NewEntityFactory(),
	}

	// Load policies
	if len(config.Policies) == 0 {
		return nil, ErrNoPolicies
	}

	for i, policyStr := range config.Policies {
		var policy cedar.Policy
		if err := policy.UnmarshalCedar([]byte(policyStr)); err != nil {
			return nil, fmt.Errorf("failed to parse policy %d: %w", i, err)
		}

		policyID := cedar.PolicyID(fmt.Sprintf("policy%d", i))
		authorizer.policySet.Add(policyID, &policy)
	}

	// Load entities if provided
	if config.EntitiesJSON != "" {
		if err := json.Unmarshal([]byte(config.EntitiesJSON), &authorizer.entities); err != nil {
			return nil, fmt.Errorf("failed to parse entities JSON: %w", err)
		}
	}

	return authorizer, nil
}

// UpdatePolicies updates the Cedar policies.
func (a *CedarAuthorizer) UpdatePolicies(policies []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(policies) == 0 {
		return ErrNoPolicies
	}

	newPolicySet := cedar.NewPolicySet()

	for i, policyStr := range policies {
		var policy cedar.Policy
		if err := policy.UnmarshalCedar([]byte(policyStr)); err != nil {
			return fmt.Errorf("failed to parse policy %d: %w", i, err)
		}

		policyID := cedar.PolicyID(fmt.Sprintf("policy%d", i))
		newPolicySet.Add(policyID, &policy)
	}

	a.policySet = newPolicySet
	return nil
}

// UpdateEntities updates the Cedar entities.
func (a *CedarAuthorizer) UpdateEntities(entitiesJSON string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var newEntities cedar.EntityMap
	if err := json.Unmarshal([]byte(entitiesJSON), &newEntities); err != nil {
		return fmt.Errorf("failed to parse entities JSON: %w", err)
	}

	a.entities = newEntities
	return nil
}

// AddEntity adds or updates an entity in the authorizer's entity store.
func (a *CedarAuthorizer) AddEntity(entity cedar.Entity) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entities[entity.UID] = entity
}

// RemoveEntity removes an entity from the authorizer's entity store.
func (a *CedarAuthorizer) RemoveEntity(uid cedar.EntityUID) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.entities, uid)
}

// GetEntity retrieves an entity from the authorizer's entity store.
func (a *CedarAuthorizer) GetEntity(uid cedar.EntityUID) (cedar.Entity, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entity, found := a.entities[uid]
	return entity, found
}

// GetEntityFactory returns the entity factory associated with this authorizer.
func (a *CedarAuthorizer) GetEntityFactory() *EntityFactory {
	return a.entityFactory
}

// IsAuthorized checks if a request is authorized.
// This is the core authorization method that all other authorization methods use.
// It takes:
// - principal: The entity making the request (e.g., "Client::vscode_extension_123")
// - action: The operation being performed (e.g., "Action::call_tool")
// - resource: The object being accessed (e.g., "Tool::weather")
// - context: Additional information about the request
// - entities: Optional Cedar entity map with attributes
func (a *CedarAuthorizer) IsAuthorized(
	principal, action, resource string,
	contextMap map[string]interface{},
	entities ...cedar.EntityMap,
) (bool, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if principal == "" {
		return false, ErrMissingPrincipal
	}

	if action == "" {
		return false, ErrMissingAction
	}

	if resource == "" {
		return false, ErrMissingResource
	}

	// Parse principal, action, and resource
	principalType, principalID, err := parseCedarEntityID(principal)
	if err != nil {
		return false, err
	}

	actionType, actionID, err := parseCedarEntityID(action)
	if err != nil {
		return false, err
	}

	resourceType, resourceID, err := parseCedarEntityID(resource)
	if err != nil {
		return false, err
	}

	// Create context record
	contextRecord := convertMapToCedarRecord(contextMap)

	// Create Cedar request
	req := cedar.Request{
		Principal: cedar.NewEntityUID(cedar.EntityType(principalType), cedar.String(principalID)),
		Action:    cedar.NewEntityUID(cedar.EntityType(actionType), cedar.String(actionID)),
		Resource:  cedar.NewEntityUID(cedar.EntityType(resourceType), cedar.String(resourceID)),
		Context:   contextRecord,
	}

	// Use the provided entities if available, otherwise use the default entities
	entityMap := a.entities
	if len(entities) > 0 && entities[0] != nil {
		// Merge the request entities with the default entities
		// This allows policies to reference both the request-specific entities
		// and any global entities defined in the authorizer
		mergedEntities := make(cedar.EntityMap)
		for k, v := range a.entities {
			mergedEntities[k] = v
		}
		for k, v := range entities[0] {
			mergedEntities[k] = v
		}

		entityMap = mergedEntities
	}

	// Debug logging for authorization
	logger.Debugf("Cedar authorization check - Principal: %s, Action: %s, Resource: %s",
		req.Principal, req.Action, req.Resource)
	logger.Debugf("Cedar context: %+v", req.Context)

	// Check authorization
	decision, diagnostic := cedar.Authorize(a.policySet, entityMap, req)

	// Log the decision
	logger.Debugf("Cedar decision: %v, diagnostic: %+v", decision, diagnostic)

	// Cedar's Authorize returns a Decision and a Diagnostic
	// Check if the Diagnostic contains any errors
	if len(diagnostic.Errors) > 0 {
		return false, fmt.Errorf("authorization error: %v", diagnostic.Errors)
	}
	return decision == cedar.Allow, nil
}

// extractClientIDFromClaims extracts the client ID from JWT claims.
// By default, it uses the "sub" (subject) claim as the client ID.
// This can be customized based on your JWT token structure.
func extractClientIDFromClaims(claims jwt.MapClaims) (string, bool) {
	// Use the GetSubject method to safely extract the "sub" claim
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return "", false
	}

	return sub, true
}

// preprocessClaims adds a "claim_" prefix to all claim keys.
// This makes it clear which values are from the JWT claims.
func preprocessClaims(claims jwt.MapClaims) map[string]interface{} {
	preprocessed := make(map[string]interface{})
	for k, v := range claims {
		claimKey := fmt.Sprintf("claim_%s", k)
		preprocessed[claimKey] = v
	}
	return preprocessed
}

// preprocessArguments adds an "arg_" prefix to all argument keys.
// For complex types, it just notes their presence with an "_present" suffix.
func preprocessArguments(arguments map[string]interface{}) map[string]interface{} {
	if arguments == nil {
		return nil
	}

	preprocessed := make(map[string]interface{})
	for k, v := range arguments {
		argKey := fmt.Sprintf("arg_%s", k)
		switch val := v.(type) {
		case string, bool, int, int64, float64:
			preprocessed[argKey] = val
		default:
			// For complex types, just note their presence
			preprocessed[argKey+"_present"] = true
		}
	}
	return preprocessed
}

// mergeContexts merges multiple context maps into a single map.
// Later maps override earlier maps if there are key conflicts.
func mergeContexts(contextMaps ...map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for _, ctxMap := range contextMaps {
		if ctxMap == nil {
			continue
		}
		for k, v := range ctxMap {
			merged[k] = v
		}
	}
	return merged
}

// authorizeToolCall authorizes a tool call operation.
// This method is used when a client tries to call a specific tool.
// It checks if the client is authorized to call the tool with the given context.
func (a *CedarAuthorizer) authorizeToolCall(
	clientID, toolName string,
	claimsMap map[string]interface{},
	attrsMap map[string]interface{},
) (bool, error) {
	// Extract principal from client ID
	principal := fmt.Sprintf("Client::%s", clientID)

	// Action is to call a tool
	action := "Action::call_tool"

	// Resource is the tool being called
	resource := fmt.Sprintf("Tool::%s", toolName)

	// Create attributes for the entities
	attributes := mergeContexts(map[string]interface{}{
		"name":      toolName,
		"operation": "call",
		"feature":   "tool",
	}, attrsMap)

	// Create Cedar entities
	entities, err := a.entityFactory.CreateEntitiesForRequest(principal, action, resource, claimsMap, attributes)
	if err != nil {
		return false, fmt.Errorf("failed to create Cedar entities: %w", err)
	}

	contextMap := mergeContexts(claimsMap, attrsMap)

	// Check authorization with entities
	return a.IsAuthorized(principal, action, resource, contextMap, entities)
}

// authorizePromptGet authorizes a prompt get operation.
// This method is used when a client tries to get a specific prompt.
// It checks if the client is authorized to access the prompt with the given context.
func (a *CedarAuthorizer) authorizePromptGet(
	clientID, promptName string,
	claimsMap map[string]interface{},
	attrsMap map[string]interface{},
) (bool, error) {
	// Extract principal from client ID
	principal := fmt.Sprintf("Client::%s", clientID)

	// Action is to get a prompt
	action := "Action::get_prompt"

	// Resource is the prompt being accessed
	resource := fmt.Sprintf("Prompt::%s", promptName)

	// Create attributes for the entities
	attributes := mergeContexts(map[string]interface{}{
		"name":      promptName,
		"operation": "get",
		"feature":   "prompt",
	}, attrsMap)

	// Create Cedar entities
	entities, err := a.entityFactory.CreateEntitiesForRequest(principal, action, resource, claimsMap, attributes)
	if err != nil {
		return false, fmt.Errorf("failed to create Cedar entities: %w", err)
	}

	contextMap := mergeContexts(claimsMap, attrsMap)

	// Check authorization with entities
	return a.IsAuthorized(principal, action, resource, contextMap, entities)
}

// authorizeResourceRead authorizes a resource read operation.
// This method is used when a client tries to read a specific resource.
// It checks if the client is authorized to read the resource.
func (a *CedarAuthorizer) authorizeResourceRead(
	clientID, resourceURI string,
	claimsMap map[string]interface{},
	attrsMap map[string]interface{},
) (bool, error) {
	// Extract principal from client ID
	principal := fmt.Sprintf("Client::%s", clientID)

	// Action is to read a resource
	action := "Action::read_resource"

	// Resource is the resource being accessed
	// Use the URI as the resource ID, but sanitize it for Cedar
	sanitizedURI := sanitizeURIForCedar(resourceURI)
	resource := fmt.Sprintf("Resource::%s", sanitizedURI)

	// Create attributes for the entities
	attributes := mergeContexts(map[string]interface{}{
		"uri":       resourceURI,
		"operation": "read",
		"feature":   "resource",
	}, attrsMap)

	// Create Cedar entities
	entities, err := a.entityFactory.CreateEntitiesForRequest(principal, action, resource, claimsMap, attributes)
	if err != nil {
		return false, fmt.Errorf("failed to create Cedar entities: %w", err)
	}

	contextMap := mergeContexts(claimsMap, attrsMap)

	// Check authorization with entities
	return a.IsAuthorized(principal, action, resource, contextMap, entities)
}

// authorizeFeatureList authorizes a list operation for a feature.
// This method is used when a client tries to list available tools, prompts, or resources.
// It checks if the client is authorized to list the specified feature type.
func (a *CedarAuthorizer) authorizeFeatureList(
	clientID string,
	feature MCPFeature,
	claimsMap map[string]interface{},
	attrsMap map[string]interface{},
) (bool, error) {
	// Extract principal from client ID
	principal := fmt.Sprintf("Client::%s", clientID)

	// Action is to list a feature
	action := fmt.Sprintf("Action::list_%ss", feature)

	// Resource is the feature type
	resource := fmt.Sprintf("FeatureType::%s", feature)

	// Create attributes for the entities
	attributes := mergeContexts(map[string]interface{}{
		"type":      string(feature),
		"operation": "list",
		"feature":   string(feature),
	}, attrsMap)

	// Create Cedar entities
	entities, err := a.entityFactory.CreateEntitiesForRequest(principal, action, resource, claimsMap, attributes)
	if err != nil {
		return false, fmt.Errorf("failed to create Cedar entities: %w", err)
	}

	contextMap := mergeContexts(claimsMap, attrsMap)

	// Check authorization with entities
	return a.IsAuthorized(principal, action, resource, contextMap, entities)
}

// parseCedarEntityID parses a Cedar entity ID in the format "Type::ID".
// It returns the type and ID parts, or an error if the format is invalid.
func parseCedarEntityID(entityID string) (string, string, error) {
	parts := strings.SplitN(entityID, "::", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid entity ID format: %s", entityID)
	}
	return parts[0], parts[1], nil
}

// sanitizeURIForCedar sanitizes a URI for use in Cedar policies.
// Cedar entity IDs have restrictions on characters, so we need to sanitize the URI.
func sanitizeURIForCedar(uri string) string {
	// Replace characters that are not allowed in Cedar entity IDs
	// This is a simple implementation - you may need to enhance it based on your needs
	replacer := strings.NewReplacer(
		":", "_",
		"/", "_",
		"\\", "_",
		"?", "_",
		"&", "_",
		"=", "_",
		"#", "_",
		" ", "_",
		".", "_",
	)
	return replacer.Replace(uri)
}

// AuthorizeWithJWTClaims demonstrates how to use JWT claims with the Cedar authorization middleware.
// This method:
// 1. Extracts JWT claims from the context
// 2. Extracts the client ID from the claims
// 3. Includes the JWT claims in the Cedar context
// 4. Creates entities with appropriate attributes
// 5. Authorizes the operation using the client ID and claims
func (a *CedarAuthorizer) AuthorizeWithJWTClaims(
	ctx context.Context,
	feature MCPFeature,
	operation MCPOperation,
	resourceID string,
	arguments map[string]interface{},
) (bool, error) {
	// Extract JWT claims from the context
	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return false, ErrMissingPrincipal
	}

	// Extract client ID from claims
	clientID, ok := extractClientIDFromClaims(claims)
	if !ok {
		return false, ErrMissingPrincipal
	}

	// Preprocess claims and arguments
	processedClaims := preprocessClaims(claims)
	processedArgs := preprocessArguments(arguments)

	// Authorize based on the feature and operation
	switch {
	case feature == MCPFeatureTool && operation == MCPOperationCall:
		// Use the authorizeToolCall function for tool call operations
		return a.authorizeToolCall(clientID, resourceID, processedClaims, processedArgs)

	case feature == MCPFeaturePrompt && operation == MCPOperationGet:
		// Use the authorizePromptGet function for prompt get operations
		return a.authorizePromptGet(clientID, resourceID, processedClaims, processedArgs)

	case feature == MCPFeatureResource && operation == MCPOperationRead:
		// Use the authorizeResourceRead function for resource read operations
		return a.authorizeResourceRead(clientID, resourceID, processedClaims, processedArgs)

	case operation == MCPOperationList:
		// Use the authorizeFeatureList function for list operations
		return a.authorizeFeatureList(clientID, feature, processedClaims, processedArgs)

	default:
		return false, fmt.Errorf("unsupported feature/operation combination: %s/%s", feature, operation)
	}
}

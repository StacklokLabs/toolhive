// Package authz provides authorization utilities using Cedar policies.
package authz

import (
	cedar "github.com/cedar-policy/cedar-go"
)

// EntityFactory creates Cedar entities for authorization.
type EntityFactory struct{}

// NewEntityFactory creates a new entity factory.
func NewEntityFactory() *EntityFactory {
	return &EntityFactory{}
}

// CreatePrincipalEntity creates a principal entity with the given ID and attributes.
func (*EntityFactory) CreatePrincipalEntity(
	principalType, principalID string,
	attributes map[string]interface{},
) (cedar.EntityUID, cedar.Entity) {
	uid := cedar.NewEntityUID(cedar.EntityType(principalType), cedar.String(principalID))
	attrs := convertMapToCedarRecord(attributes)

	entity := cedar.Entity{
		UID:        uid,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: attrs,
		Tags:       cedar.NewRecord(cedar.RecordMap{}),
	}

	return uid, entity
}

// CreateActionEntity creates an action entity with the given ID and attributes.
func (*EntityFactory) CreateActionEntity(
	actionType, actionID string,
	attributes map[string]interface{},
) (cedar.EntityUID, cedar.Entity) {
	uid := cedar.NewEntityUID(cedar.EntityType(actionType), cedar.String(actionID))

	// Ensure operation attribute is set
	if attributes == nil {
		attributes = make(map[string]interface{})
	}
	attributes["operation"] = actionID

	attrs := convertMapToCedarRecord(attributes)

	entity := cedar.Entity{
		UID:        uid,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: attrs,
		Tags:       cedar.NewRecord(cedar.RecordMap{}),
	}

	return uid, entity
}

// CreateResourceEntity creates a resource entity with the given ID and attributes.
func (*EntityFactory) CreateResourceEntity(
	resourceType, resourceID string,
	attributes map[string]interface{},
) (cedar.EntityUID, cedar.Entity) {
	uid := cedar.NewEntityUID(cedar.EntityType(resourceType), cedar.String(resourceID))

	// Ensure name attribute is set
	if attributes == nil {
		attributes = make(map[string]interface{})
	}
	attributes["name"] = resourceID

	attrs := convertMapToCedarRecord(attributes)

	entity := cedar.Entity{
		UID:        uid,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: attrs,
		Tags:       cedar.NewRecord(cedar.RecordMap{}),
	}

	return uid, entity
}

// CreateEntitiesForRequest creates entities for a specific authorization request.
func (f *EntityFactory) CreateEntitiesForRequest(
	principal, action, resource string,
	claimsMap map[string]interface{},
	attributes map[string]interface{},
) (cedar.EntityMap, error) {
	// Parse principal, action, and resource
	principalType, principalID, err := parseCedarEntityID(principal)
	if err != nil {
		return nil, err
	}

	actionType, actionID, err := parseCedarEntityID(action)
	if err != nil {
		return nil, err
	}

	resourceType, resourceID, err := parseCedarEntityID(resource)
	if err != nil {
		return nil, err
	}

	// Create Cedar entities
	entities := make(cedar.EntityMap)

	// Create principal entity
	principalUID, principalEntity := f.CreatePrincipalEntity(principalType, principalID, claimsMap)
	entities[principalUID] = principalEntity

	// Create action entity
	actionUID, actionEntity := f.CreateActionEntity(actionType, actionID, nil)
	entities[actionUID] = actionEntity

	// Create resource entity
	resourceUID, resourceEntity := f.CreateResourceEntity(resourceType, resourceID, attributes)
	entities[resourceUID] = resourceEntity

	return entities, nil
}

// convertMapToCedarRecord converts a Go map to a Cedar record.
func convertMapToCedarRecord(data map[string]interface{}) cedar.Record {
	if data == nil {
		return cedar.NewRecord(cedar.RecordMap{})
	}

	recordMap := make(cedar.RecordMap)

	for k, v := range data {
		// Convert Go values to Cedar values
		cedarValue := convertToCedarValue(v)
		if cedarValue != nil {
			recordMap[cedar.String(k)] = cedarValue
		}
	}

	return cedar.NewRecord(recordMap)
}

// convertToCedarValue converts a Go value to a Cedar value.
func convertToCedarValue(v interface{}) cedar.Value {
	switch val := v.(type) {
	case bool:
		return convertBoolToCedar(val)
	case string:
		return cedar.String(val)
	case int:
		return cedar.Long(val)
	case int64:
		return cedar.Long(val)
	case float64:
		return convertFloatToCedar(val)
	case []interface{}:
		return convertInterfaceArrayToCedar(val)
	case []string:
		return convertStringArrayToCedar(val)
	default:
		// Skip unsupported types
		return nil
	}
}

// convertBoolToCedar converts a bool to a Cedar value.
func convertBoolToCedar(val bool) cedar.Value {
	if val {
		return cedar.True
	}
	return cedar.False
}

// convertFloatToCedar converts a float64 to a Cedar decimal value.
func convertFloatToCedar(val float64) cedar.Value {
	decimalVal, err := cedar.NewDecimalFromFloat(val)
	if err != nil {
		return nil
	}
	return decimalVal
}

// convertInterfaceArrayToCedar converts an array of interfaces to a Cedar set.
func convertInterfaceArrayToCedar(val []interface{}) cedar.Value {
	values := make([]cedar.Value, 0, len(val))
	for _, item := range val {
		cedarItem := convertArrayItemToCedar(item)
		if cedarItem != nil {
			values = append(values, cedarItem)
		}
	}
	return cedar.NewSet(values...)
}

// convertArrayItemToCedar converts an array item to a Cedar value.
func convertArrayItemToCedar(item interface{}) cedar.Value {
	switch itemVal := item.(type) {
	case string:
		return cedar.String(itemVal)
	case bool:
		return convertBoolToCedar(itemVal)
	case int:
		return cedar.Long(itemVal)
	case int64:
		return cedar.Long(itemVal)
	case float64:
		return convertFloatToCedar(itemVal)
	default:
		return nil
	}
}

// convertStringArrayToCedar converts a string array to a Cedar set.
func convertStringArrayToCedar(val []string) cedar.Value {
	values := make([]cedar.Value, 0, len(val))
	for _, item := range val {
		values = append(values, cedar.String(item))
	}
	return cedar.NewSet(values...)
}

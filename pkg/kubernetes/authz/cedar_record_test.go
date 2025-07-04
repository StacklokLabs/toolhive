package authz

import (
	"math"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/stretchr/testify/assert"
)

// TestConvertMapToCedarRecord tests the convertMapToCedarRecord function.
func TestConvertMapToCedarRecord(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]cedar.Value // Expected key-value pairs in the record
	}{
		{
			name:     "Empty map",
			input:    map[string]interface{}{},
			expected: map[string]cedar.Value{},
		},
		{
			name: "Boolean values",
			input: map[string]interface{}{
				"true_value":  true,
				"false_value": false,
			},
			expected: map[string]cedar.Value{
				"true_value":  cedar.True,
				"false_value": cedar.False,
			},
		},
		{
			name: "String values",
			input: map[string]interface{}{
				"string1": "hello",
				"string2": "world",
			},
			expected: map[string]cedar.Value{
				"string1": cedar.String("hello"),
				"string2": cedar.String("world"),
			},
		},
		{
			name: "Integer values",
			input: map[string]interface{}{
				"int1": 42,
				"int2": int64(9223372036854775807),
			},
			expected: map[string]cedar.Value{
				"int1": cedar.Long(42),
				"int2": cedar.Long(9223372036854775807),
			},
		},
		{
			name: "Float values",
			input: map[string]interface{}{
				"float1": 3.14,
				"float2": 2.71828,
			},
			expected: func() map[string]cedar.Value {
				decimal1, _ := cedar.NewDecimalFromFloat(3.14)
				decimal2, _ := cedar.NewDecimalFromFloat(2.71828)
				return map[string]cedar.Value{
					"float1": decimal1,
					"float2": decimal2,
				}
			}(),
		},
		{
			name: "String array values",
			input: map[string]interface{}{
				"roles": []string{"admin", "user", "guest"},
			},
			expected: map[string]cedar.Value{
				"roles": cedar.NewSet(
					cedar.String("admin"),
					cedar.String("user"),
					cedar.String("guest"),
				),
			},
		},
		{
			name: "Interface array values",
			input: map[string]interface{}{
				"mixed": []interface{}{"string", 42, true, 3.14},
			},
			expected: func() map[string]cedar.Value {
				decimal, _ := cedar.NewDecimalFromFloat(3.14)
				return map[string]cedar.Value{
					"mixed": cedar.NewSet(
						cedar.String("string"),
						cedar.Long(42),
						cedar.True,
						decimal,
					),
				}
			}(),
		},
		{
			name: "Mixed types",
			input: map[string]interface{}{
				"string":  "hello",
				"int":     42,
				"bool":    true,
				"float":   3.14,
				"array":   []string{"a", "b", "c"},
				"mixed":   []interface{}{1, "two", true},
				"ignored": map[string]string{"key": "value"}, // Should be ignored
			},
			expected: func() map[string]cedar.Value {
				decimal, _ := cedar.NewDecimalFromFloat(3.14)
				return map[string]cedar.Value{
					"string": cedar.String("hello"),
					"int":    cedar.Long(42),
					"bool":   cedar.True,
					"float":  decimal,
					"array": cedar.NewSet(
						cedar.String("a"),
						cedar.String("b"),
						cedar.String("c"),
					),
					"mixed": cedar.NewSet(
						cedar.Long(1),
						cedar.String("two"),
						cedar.True,
					),
					// "ignored" key should not be present
				}
			}(),
		},
		{
			name: "Invalid float in array",
			input: map[string]interface{}{
				"mixed": []interface{}{1, "two", true, math.Inf(1)}, // Infinity is not valid for Cedar decimal
			},
			expected: map[string]cedar.Value{
				"mixed": cedar.NewSet(
					cedar.Long(1),
					cedar.String("two"),
					cedar.True,
					// Infinity should be skipped
				),
			},
		},
		{
			name: "Invalid float value",
			input: map[string]interface{}{
				"invalid_float": math.Inf(1), // Infinity is not valid for Cedar decimal
			},
			expected: map[string]cedar.Value{
				// No entries expected as the invalid float should be skipped
			},
		},
		{
			name: "Unsupported types",
			input: map[string]interface{}{
				"map":    map[string]interface{}{"nested": "value"},
				"struct": struct{ Name string }{"test"},
				"valid":  "this should be included",
			},
			expected: map[string]cedar.Value{
				"valid": cedar.String("this should be included"),
				// Other keys should be skipped
			},
		},
		{
			name:     "Nil input",
			input:    nil,
			expected: map[string]cedar.Value{},
		},
		{
			name: "Array with false boolean",
			input: map[string]interface{}{
				"bools": []interface{}{false},
			},
			expected: map[string]cedar.Value{
				"bools": cedar.NewSet(cedar.False),
			},
		},
		{
			name: "Array with int64 value",
			input: map[string]interface{}{
				"int64s": []interface{}{int64(9223372036854775807)},
			},
			expected: map[string]cedar.Value{
				"int64s": cedar.NewSet(cedar.Long(9223372036854775807)),
			},
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a Cedar record
			record := convertMapToCedarRecord(tc.input)

			// Check that the record has the expected keys and values
			assert.Equal(t, len(tc.expected), record.Len(), "Record has wrong number of entries")

			for k, expectedValue := range tc.expected {
				cedarKey := cedar.String(k)
				actualValue, ok := record.Get(cedarKey)
				assert.True(t, ok, "Key %s not found in record map", k)

				// For decimal values, we need to compare the string representation
				// because Decimal.Equal() is not implemented
				if _, ok := expectedValue.(cedar.Decimal); ok {
					assert.Equal(t, expectedValue.String(), actualValue.String(), "Value for key %s does not match", k)
				} else {
					assert.Equal(t, expectedValue, actualValue, "Value for key %s does not match", k)
				}
			}
		})
	}
}

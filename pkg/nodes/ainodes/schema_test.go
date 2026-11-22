package ainodes

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateJSONValue pins the subset the response-schema check enforces:
// types, required properties, nested objects, array items, and enums — and
// that vocabulary beyond the subset is ignored rather than rejected.
func TestValidateJSONValue(t *testing.T) {
	schema := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(`{
		"type": "object",
		"required": ["room", "actions"],
		"additionalProperties": false,
		"properties": {
			"room": {"type": "string"},
			"level": {"type": ["integer", "null"]},
			"mood": {"enum": ["calm", "bright"]},
			"actions": {
				"type": "array",
				"items": {
					"type": "object",
					"required": ["device"],
					"properties": {"device": {"type": "string"}, "on": {"type": "boolean"}}
				}
			}
		}
	}`), &schema))
	// Programmatic schemas use []string while JSON-decoded schemas use []any.
	schema["required"] = []string{"room", "actions"}
	props := schema["properties"].(map[string]any)
	props["level"].(map[string]any)["type"] = []string{"integer", "null"}
	props["mood"].(map[string]any)["enum"] = []string{"calm", "bright"}

	decode := func(raw string) any {
		var v any
		require.NoError(t, json.Unmarshal([]byte(raw), &v))
		return v
	}

	assert.NoError(t, validateJSONValue(decode(`{
		"room": "kitchen", "level": 3, "mood": "calm",
		"actions": [{"device": "lamp", "on": true}]
	}`), schema), "a conforming reply passes")

	assert.NoError(t, validateJSONValue(decode(`{
		"room": "kitchen", "level": null, "actions": [], "extra": 1
	}`), schema), "type alternatives and unchecked keywords are tolerated")

	err := validateJSONValue(decode(`{"room": "kitchen"}`), schema)
	require.Error(t, err, "a missing required property fails")
	assert.Contains(t, err.Error(), `"actions"`)

	err = validateJSONValue(decode(`{"room": 4, "actions": []}`), schema)
	require.Error(t, err, "a wrong property type fails")
	assert.Contains(t, err.Error(), "$.room")

	err = validateJSONValue(decode(`{"room": "k", "level": 1.5, "actions": []}`), schema)
	require.Error(t, err, "a fractional number is not an integer")

	err = validateJSONValue(decode(`{"room": "k", "mood": "loud", "actions": []}`), schema)
	require.Error(t, err, "a value outside the enum fails")

	err = validateJSONValue(decode(`{"room": "k", "actions": [{"on": true}]}`), schema)
	require.Error(t, err, "array items are checked element by element")
	assert.Contains(t, err.Error(), "actions[0]")
}

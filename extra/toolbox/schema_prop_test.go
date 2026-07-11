package toolbox

import (
	"encoding/json"
	"reflect"
	"testing"

	bond "github.com/nisimpson/bond"
)

// TestProperty_SchemaRoundTrip verifies that for each toolbox tool, serializing
// InputSchema() to JSON and deserializing back produces a deeply equal map.
// It also verifies that the serialized JSON contains the required keys "type",
// "properties", and "required".
//
// **Validates: Requirements TBOX-7.6, TBOX-7.7**
func TestProperty_SchemaRoundTrip(t *testing.T) {
	tools := []bond.Tool{
		newShellTool(nil),
		newHTTPTool(0),
		newFileReadTool(nil),
		newFileWriteTool(nil),
		newEnvTool(nil),
	}

	for _, tool := range tools {
		t.Run(tool.Name(), func(t *testing.T) {
			// 1. Serialize the schema to JSON.
			data, err := json.Marshal(tool.InputSchema())
			if err != nil {
				t.Fatalf("failed to marshal InputSchema: %v", err)
			}

			// 2. Verify JSON is parseable and deserialize into a map.
			var firstParse map[string]any
			if err := json.Unmarshal(data, &firstParse); err != nil {
				t.Fatalf("failed to unmarshal schema JSON: %v", err)
			}

			// 3. Verify required keys exist (TBOX-7.7).
			for _, key := range []string{"type", "properties", "required"} {
				if _, ok := firstParse[key]; !ok {
					t.Errorf("schema missing required key %q", key)
				}
			}

			// 4. Round-trip: re-serialize and re-deserialize, verify deep equality (TBOX-7.6).
			data2, err := json.Marshal(firstParse)
			if err != nil {
				t.Fatalf("failed to re-marshal: %v", err)
			}
			var secondParse map[string]any
			if err := json.Unmarshal(data2, &secondParse); err != nil {
				t.Fatalf("failed to re-unmarshal: %v", err)
			}

			if !reflect.DeepEqual(firstParse, secondParse) {
				t.Errorf("schema round-trip produced different results for tool %q", tool.Name())
			}
		})
	}
}

package builtin

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

// **Validates: Requirements FTE-5.1, FTE-5.2, FTE-5.3, FTE-5.4, FTE-5.5**

// TestFileToolSchema_Serialization verifies that the input schemas for
// FileReadInput and FileWriteInput serialize correctly to JSON, containing
// the expected properties with correct types, constraints, and enum values.
// It also verifies lossless round-trip serialization.
func TestFileToolSchema_Serialization(t *testing.T) {
	t.Run("FileReadInput_schema_contains_start_line_and_end_line", func(t *testing.T) {
		// FTE-5.1, FTE-5.3: Verify start_line and end_line appear with correct type and minimum
		schema := fileReadInputSchema()
		data, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("failed to marshal FileReadInput schema: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("failed to unmarshal schema JSON: %v", err)
		}

		// Verify top-level type is "object"
		if parsed["type"] != "object" {
			t.Errorf("expected type 'object', got %v", parsed["type"])
		}

		// Verify "required" contains only "path"
		required, ok := parsed["required"].([]any)
		if !ok {
			t.Fatalf("expected 'required' to be an array, got %T", parsed["required"])
		}
		if len(required) != 1 || required[0] != "path" {
			t.Errorf("expected required to be [\"path\"], got %v", required)
		}

		// Verify properties exist
		properties, ok := parsed["properties"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'properties' to be a map, got %T", parsed["properties"])
		}

		// Verify start_line property
		startLine, ok := properties["start_line"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'start_line' property to exist as a map, got %T", properties["start_line"])
		}
		if startLine["type"] != "integer" {
			t.Errorf("expected start_line type 'integer', got %v", startLine["type"])
		}
		if min, ok := startLine["minimum"]; !ok || min != float64(1) {
			t.Errorf("expected start_line minimum 1, got %v", startLine["minimum"])
		}

		// Verify end_line property
		endLine, ok := properties["end_line"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'end_line' property to exist as a map, got %T", properties["end_line"])
		}
		if endLine["type"] != "integer" {
			t.Errorf("expected end_line type 'integer', got %v", endLine["type"])
		}
		if min, ok := endLine["minimum"]; !ok || min != float64(1) {
			t.Errorf("expected end_line minimum 1, got %v", endLine["minimum"])
		}

		// Verify path property exists
		pathProp, ok := properties["path"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'path' property to exist as a map, got %T", properties["path"])
		}
		if pathProp["type"] != "string" {
			t.Errorf("expected path type 'string', got %v", pathProp["type"])
		}
	})

	t.Run("FileWriteInput_schema_contains_mode_enum_and_patch_fields", func(t *testing.T) {
		// FTE-5.2, FTE-5.4: Verify mode, old_text, new_text, patches appear correctly
		schema := fileWriteInputSchema()
		data, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("failed to marshal FileWriteInput schema: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("failed to unmarshal schema JSON: %v", err)
		}

		// Verify top-level type is "object"
		if parsed["type"] != "object" {
			t.Errorf("expected type 'object', got %v", parsed["type"])
		}

		// Verify "required" contains only "path"
		required, ok := parsed["required"].([]any)
		if !ok {
			t.Fatalf("expected 'required' to be an array, got %T", parsed["required"])
		}
		if len(required) != 1 || required[0] != "path" {
			t.Errorf("expected required to be [\"path\"], got %v", required)
		}

		// Verify properties exist
		properties, ok := parsed["properties"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'properties' to be a map, got %T", parsed["properties"])
		}

		// Verify mode property with enum constraint
		mode, ok := properties["mode"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'mode' property to exist as a map, got %T", properties["mode"])
		}
		if mode["type"] != "string" {
			t.Errorf("expected mode type 'string', got %v", mode["type"])
		}
		modeEnum, ok := mode["enum"].([]any)
		if !ok {
			t.Fatalf("expected mode 'enum' to be an array, got %T", mode["enum"])
		}
		expectedEnum := map[string]bool{"write": false, "replace": false, "patch": false}
		for _, v := range modeEnum {
			s, ok := v.(string)
			if !ok {
				t.Errorf("expected enum value to be string, got %T", v)
				continue
			}
			if _, exists := expectedEnum[s]; exists {
				expectedEnum[s] = true
			}
		}
		for k, found := range expectedEnum {
			if !found {
				t.Errorf("expected mode enum to contain %q", k)
			}
		}

		// Verify old_text property exists
		if _, ok := properties["old_text"].(map[string]any); !ok {
			t.Errorf("expected 'old_text' property to exist as a map, got %T", properties["old_text"])
		}

		// Verify new_text property exists
		if _, ok := properties["new_text"].(map[string]any); !ok {
			t.Errorf("expected 'new_text' property to exist as a map, got %T", properties["new_text"])
		}

		// Verify patches property exists with items
		patches, ok := properties["patches"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'patches' property to exist as a map, got %T", properties["patches"])
		}
		if patches["type"] != "array" {
			t.Errorf("expected patches type 'array', got %v", patches["type"])
		}
		if _, ok := patches["items"]; !ok {
			t.Error("expected 'patches' to have 'items' key")
		}
	})

	t.Run("FileReadInput_round_trip_serialization", func(t *testing.T) {
		// FTE-5.5: Verify round-trip serialization produces deeply equal structures
		schema := fileReadInputSchema()
		data, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("failed to marshal FileReadInput schema: %v", err)
		}

		// First unmarshal
		var firstParse map[string]any
		if err := json.Unmarshal(data, &firstParse); err != nil {
			t.Fatalf("failed to unmarshal schema: %v", err)
		}

		// Re-marshal from the parsed map
		data2, err := json.Marshal(firstParse)
		if err != nil {
			t.Fatalf("failed to re-marshal: %v", err)
		}

		// Second unmarshal
		var secondParse map[string]any
		if err := json.Unmarshal(data2, &secondParse); err != nil {
			t.Fatalf("failed to re-unmarshal: %v", err)
		}

		// Compare
		if !reflect.DeepEqual(firstParse, secondParse) {
			t.Errorf("round-trip serialization produced different results for FileReadInput schema")
		}
	})

	t.Run("FileWriteInput_round_trip_serialization", func(t *testing.T) {
		// FTE-5.5: Verify round-trip serialization produces deeply equal structures
		schema := fileWriteInputSchema()
		data, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("failed to marshal FileWriteInput schema: %v", err)
		}

		// First unmarshal
		var firstParse map[string]any
		if err := json.Unmarshal(data, &firstParse); err != nil {
			t.Fatalf("failed to unmarshal schema: %v", err)
		}

		// Re-marshal from the parsed map
		data2, err := json.Marshal(firstParse)
		if err != nil {
			t.Fatalf("failed to re-marshal: %v", err)
		}

		// Second unmarshal
		var secondParse map[string]any
		if err := json.Unmarshal(data2, &secondParse); err != nil {
			t.Fatalf("failed to re-unmarshal: %v", err)
		}

		// Compare
		if !reflect.DeepEqual(firstParse, secondParse) {
			t.Errorf("round-trip serialization produced different results for FileWriteInput schema")
		}
	})
}

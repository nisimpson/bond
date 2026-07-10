package tool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

type AddInput struct {
	A int `json:"a" jsonschema:"first number"`
	B int `json:"b" jsonschema:"second number"`
}

type PersonInput struct {
	Name string `json:"name" jsonschema:"person name"`
	Age  int    `json:"age" jsonschema:"person age"`
}

func TestFor_BasicStruct(t *testing.T) {
	s := tool.SchemaFor[AddInput]()
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty schema")
	}

	// Should contain property names
	str := string(data)
	if !strings.Contains(str, `"a"`) {
		t.Fatalf("expected schema to contain 'a', got %q", str)
	}
	if !strings.Contains(str, `"b"`) {
		t.Fatalf("expected schema to contain 'b', got %q", str)
	}
}

func TestFor_PersonStruct(t *testing.T) {
	s := tool.SchemaFor[PersonInput]()
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}
	str := string(data)
	if !strings.Contains(str, `"name"`) {
		t.Fatalf("expected schema to contain 'name', got %q", str)
	}
	if !strings.Contains(str, `"age"`) {
		t.Fatalf("expected schema to contain 'age', got %q", str)
	}
}

func TestSchema_Validate_Valid(t *testing.T) {
	s := tool.SchemaFor[AddInput]()
	data := map[string]any{"a": 1, "b": 2}
	if err := s.Validate(data); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestSchema_Validate_Invalid(t *testing.T) {
	s := tool.SchemaFor[AddInput]()
	// "a" should be integer, not string
	data := map[string]any{"a": "not a number", "b": 2}
	err := s.Validate(data)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestSchema_MarshalJSON_IsValidJSON(t *testing.T) {
	s := tool.SchemaFor[AddInput]()
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

// --- Structured Output Tests ---

// mockTool implements bond.Tool for testing structured output.
type mockTool struct {
	name        string
	description string
	output      string
	err         error
}

func (m *mockTool) Name() string                { return m.name }
func (m *mockTool) Description() string         { return m.description }
func (m *mockTool) InputSchema() json.Marshaler { return json.RawMessage(`{"type":"object"}`) }
func (m *mockTool) Run(_ context.Context, _ json.RawMessage) ([]bond.Block, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []bond.Block{&bond.TextBlock{Text: m.output}}, nil
}

type OutputSchema struct {
	Result int `json:"result"`
}

func TestEnforceStructuredOutput_Valid(t *testing.T) {
	baseTool := &mockTool{
		name:        "add",
		description: "adds numbers",
		output:      `{"result": 42}`,
	}

	wrapped := tool.EnforceStructuredOutput[OutputSchema](baseTool)

	blocks, err := wrapped.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	if tb.Text != `{"result": 42}` {
		t.Fatalf("unexpected text: %q", tb.Text)
	}
}

func TestEnforceStructuredOutput_InvalidJSON(t *testing.T) {
	baseTool := &mockTool{
		name:        "add",
		description: "adds numbers",
		output:      `not json at all`,
	}

	wrapped := tool.EnforceStructuredOutput[OutputSchema](baseTool)

	_, err := wrapped.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON' in error, got %q", err.Error())
	}
}

func TestEnforceStructuredOutput_SchemaViolation(t *testing.T) {
	baseTool := &mockTool{
		name:        "add",
		description: "adds numbers",
		output:      `{"result": "not a number"}`,
	}

	wrapped := tool.EnforceStructuredOutput[OutputSchema](baseTool)

	_, err := wrapped.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected 'validation failed' in error, got %q", err.Error())
	}
}

func TestEnforceStructuredOutput_InnerError(t *testing.T) {
	baseTool := &mockTool{
		name:        "add",
		description: "adds numbers",
		err:         context.DeadlineExceeded,
	}

	wrapped := tool.EnforceStructuredOutput[OutputSchema](baseTool)

	_, err := wrapped.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestEnforceStructuredOutput_PreservesMetadata(t *testing.T) {
	baseTool := &mockTool{
		name:        "my-tool",
		description: "my description",
		output:      `{"result": 1}`,
	}

	wrapped := tool.EnforceStructuredOutput[OutputSchema](baseTool)

	if wrapped.Name() != "my-tool" {
		t.Fatalf("expected 'my-tool', got %q", wrapped.Name())
	}
	if wrapped.Description() != "my description" {
		t.Fatalf("expected 'my description', got %q", wrapped.Description())
	}
	if wrapped.InputSchema() == nil {
		t.Fatal("expected non-nil InputSchema")
	}
}

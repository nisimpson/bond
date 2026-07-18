package gemini_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/internal/gemini"
)

func TestMapMessages_UserText(t *testing.T) {
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
	}

	result := gemini.MapMessages(messages)

	if len(result) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result))
	}

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var content struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(data, &content); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if content.Role != "user" {
		t.Errorf("role = %q, want %q", content.Role, "user")
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	if content.Parts[0].Text != "hello" {
		t.Errorf("text = %q, want %q", content.Parts[0].Text, "hello")
	}
}

func TestMapMessages_ModelFunctionCall(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleAssistant,
			Content: []bond.Block{
				&bond.ToolUseBlock{
					ID:    "call_1",
					Name:  "get_weather",
					Input: json.RawMessage(`{"city":"NYC"}`),
				},
			},
		},
	}

	result := gemini.MapMessages(messages)

	if len(result) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result))
	}

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var content struct {
		Role  string `json:"role"`
		Parts []struct {
			FunctionCall *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall,omitempty"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(data, &content); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if content.Role != "model" {
		t.Errorf("role = %q, want %q", content.Role, "model")
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	fc := content.Parts[0].FunctionCall
	if fc == nil {
		t.Fatal("expected functionCall part, got nil")
	}
	if fc.Name != "get_weather" {
		t.Errorf("name = %q, want %q", fc.Name, "get_weather")
	}
	if string(fc.Args) != `{"city":"NYC"}` {
		t.Errorf("args = %s, want %s", fc.Args, `{"city":"NYC"}`)
	}
}

func TestMapMessages_FunctionResponse(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleUser,
			Content: []bond.Block{
				&bond.ToolResultBlock{
					ToolUseID: "get_weather",
					Content:   []bond.Block{&bond.TextBlock{Text: `{"temp":"72F"}`}},
				},
			},
		},
	}

	result := gemini.MapMessages(messages)

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var content struct {
		Role  string `json:"role"`
		Parts []struct {
			FunctionResponse *struct {
				Name     string          `json:"name"`
				Response json.RawMessage `json:"response"`
			} `json:"functionResponse,omitempty"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(data, &content); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if content.Role != "user" {
		t.Errorf("role = %q, want %q", content.Role, "user")
	}
	fr := content.Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("expected functionResponse part, got nil")
	}
	if fr.Name != "get_weather" {
		t.Errorf("name = %q, want %q", fr.Name, "get_weather")
	}
	// The response should be valid JSON containing our temp
	if string(fr.Response) != `{"temp":"72F"}` {
		t.Errorf("response = %s, want %s", fr.Response, `{"temp":"72F"}`)
	}
}

func TestMapTools_Empty(t *testing.T) {
	result, err := gemini.MapTools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestMapTools_WithTools(t *testing.T) {
	tools := []bond.Tool{
		&fakeTool{name: "search", desc: "Search the web", schema: map[string]any{"type": "object"}},
	}

	result, err := gemini.MapTools(tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 tool config, got %d", len(result))
	}

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var tc struct {
		FunctionDeclarations []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"functionDeclarations"`
	}
	if err := json.Unmarshal(data, &tc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(tc.FunctionDeclarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(tc.FunctionDeclarations))
	}
	decl := tc.FunctionDeclarations[0]
	if decl.Name != "search" {
		t.Errorf("name = %q, want %q", decl.Name, "search")
	}
	if decl.Description != "Search the web" {
		t.Errorf("description = %q, want %q", decl.Description, "Search the web")
	}
}

// fakeTool implements bond.Tool for testing.
type fakeTool struct {
	name   string
	desc   string
	schema any
}

func (f *fakeTool) Name() string                { return f.name }
func (f *fakeTool) Description() string         { return f.desc }
func (f *fakeTool) InputSchema() json.Marshaler { return jsonValue{f.schema} }
func (f *fakeTool) Run(_ context.Context, _ json.RawMessage) ([]bond.Block, error) {
	return nil, nil
}

type jsonValue struct{ v any }

func (j jsonValue) MarshalJSON() ([]byte, error) { return json.Marshal(j.v) }

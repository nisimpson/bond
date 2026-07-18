package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/internal/anthropic"
)

func TestMapMessages_UserText(t *testing.T) {
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
	}

	result := anthropic.MapMessages(messages)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var msg struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if msg.Role != "user" {
		t.Errorf("role = %q, want %q", msg.Role, "user")
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != "text" {
		t.Errorf("type = %q, want %q", msg.Content[0].Type, "text")
	}
	if msg.Content[0].Text != "hello" {
		t.Errorf("text = %q, want %q", msg.Content[0].Text, "hello")
	}
}

func TestMapMessages_AssistantToolUse(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleAssistant,
			Content: []bond.Block{
				&bond.ToolUseBlock{
					ID:    "tool_123",
					Name:  "get_weather",
					Input: json.RawMessage(`{"city":"NYC"}`),
				},
			},
		},
	}

	result := anthropic.MapMessages(messages)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var msg struct {
		Role    string `json:"role"`
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if msg.Role != "assistant" {
		t.Errorf("role = %q, want %q", msg.Role, "assistant")
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}

	block := msg.Content[0]
	if block.Type != "tool_use" {
		t.Errorf("type = %q, want %q", block.Type, "tool_use")
	}
	if block.ID != "tool_123" {
		t.Errorf("id = %q, want %q", block.ID, "tool_123")
	}
	if block.Name != "get_weather" {
		t.Errorf("name = %q, want %q", block.Name, "get_weather")
	}
	if string(block.Input) != `{"city":"NYC"}` {
		t.Errorf("input = %s, want %s", block.Input, `{"city":"NYC"}`)
	}
}

func TestMapMessages_ToolResult(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleUser,
			Content: []bond.Block{
				&bond.ToolResultBlock{
					ToolUseID: "tool_123",
					Content:   []bond.Block{&bond.TextBlock{Text: "72°F"}},
				},
			},
		},
	}

	result := anthropic.MapMessages(messages)

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var msg struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"is_error"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if msg.Role != "user" {
		t.Errorf("role = %q, want %q", msg.Role, "user")
	}

	block := msg.Content[0]
	if block.Type != "tool_result" {
		t.Errorf("type = %q, want %q", block.Type, "tool_result")
	}
	if block.ToolUseID != "tool_123" {
		t.Errorf("tool_use_id = %q, want %q", block.ToolUseID, "tool_123")
	}
	if len(block.Content) != 1 || block.Content[0].Text != "72°F" {
		t.Errorf("content text = %v, want [{text: 72°F}]", block.Content)
	}
}

func TestMapMessages_ToolResult_IsError(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleUser,
			Content: []bond.Block{
				&bond.ToolResultBlock{
					ToolUseID: "tool_456",
					Content:   []bond.Block{&bond.TextBlock{Text: "not found"}},
					IsError:   true,
				},
			},
		},
	}

	result := anthropic.MapMessages(messages)

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var msg struct {
		Content []struct {
			IsError bool `json:"is_error"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !msg.Content[0].IsError {
		t.Error("expected is_error to be true")
	}
}

func TestMapTools_Empty(t *testing.T) {
	result, err := anthropic.MapTools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestMapTools_WithTools(t *testing.T) {
	tools := []bond.Tool{
		&fakeTool{name: "search", desc: "Search the web", schema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}},
	}

	result, err := anthropic.MapTools(tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}

	data, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var tool struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(data, &tool); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if tool.Name != "search" {
		t.Errorf("name = %q, want %q", tool.Name, "search")
	}
	if tool.Description != "Search the web" {
		t.Errorf("description = %q, want %q", tool.Description, "Search the web")
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
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

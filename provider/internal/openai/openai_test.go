package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	bond "github.com/nisimpson/bond"
)

// buildSSEResponse constructs a synthetic SSE body from chunks.
func buildSSEResponse(chunks ...ChatCompletionChunk) io.ReadCloser {
	var buf bytes.Buffer
	for _, c := range chunks {
		data, _ := json.Marshal(c)
		buf.WriteString("data: ")
		buf.Write(data)
		buf.WriteString("\n\n")
	}
	buf.WriteString("data: [DONE]\n\n")
	return io.NopCloser(&buf)
}

// mockTool implements bond.Tool for testing MapTools.
type mockTool struct {
	name        string
	description string
	schema      json.RawMessage
}

func (m mockTool) Name() string                { return m.name }
func (m mockTool) Description() string         { return m.description }
func (m mockTool) InputSchema() json.Marshaler { return m.schema }
func (m mockTool) Run(_ context.Context, _ json.RawMessage) ([]bond.Block, error) {
	return nil, nil
}

// --------------------------------------------------------------------------
// Tests for MapMessages
// Validates: 2.1, 2.2, 2.3, 2.4, 2.5
// --------------------------------------------------------------------------

func TestMapMessages_UserText(t *testing.T) {
	messages := []bond.Message{
		{
			Role:    bond.RoleUser,
			Content: []bond.Block{&bond.TextBlock{Text: "hello"}},
		},
	}

	result := MapMessages(messages, "")

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", result[0].Role)
	}
	if result[0].Content != "hello" {
		t.Errorf("expected content 'hello', got %q", result[0].Content)
	}
}

func TestMapMessages_AssistantWithToolCalls(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleAssistant,
			Content: []bond.Block{
				&bond.TextBlock{Text: "Let me check that."},
				&bond.ToolUseBlock{
					ID:    "call_123",
					Name:  "get_weather",
					Input: json.RawMessage(`{"city":"Seattle"}`),
				},
			},
		},
	}

	result := MapMessages(messages, "")

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	msg := result[0]
	if msg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", msg.Role)
	}
	if msg.Content != "Let me check that." {
		t.Errorf("expected content 'Let me check that.', got %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_123" {
		t.Errorf("expected tool call ID 'call_123', got %q", tc.ID)
	}
	if tc.Type != "function" {
		t.Errorf("expected type 'function', got %q", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Seattle"}` {
		t.Errorf("expected arguments %q, got %q", `{"city":"Seattle"}`, tc.Function.Arguments)
	}
}

func TestMapMessages_ToolResult(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleUser,
			Content: []bond.Block{
				&bond.ToolResultBlock{
					ToolUseID: "call_123",
					Content:   []bond.Block{&bond.TextBlock{Text: "72°F and sunny"}},
				},
			},
		},
	}

	result := MapMessages(messages, "")

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	msg := result[0]
	if msg.Role != "tool" {
		t.Errorf("expected role 'tool', got %q", msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("expected tool_call_id 'call_123', got %q", msg.ToolCallID)
	}
	if msg.Content != "72°F and sunny" {
		t.Errorf("expected content '72°F and sunny', got %q", msg.Content)
	}
}

func TestMapMessages_MultipleBlocks(t *testing.T) {
	messages := []bond.Message{
		{
			Role: bond.RoleAssistant,
			Content: []bond.Block{
				&bond.TextBlock{Text: "Part 1. "},
				&bond.TextBlock{Text: "Part 2."},
				&bond.ToolUseBlock{
					ID:    "call_a",
					Name:  "search",
					Input: json.RawMessage(`{"q":"go"}`),
				},
				&bond.ToolUseBlock{
					ID:    "call_b",
					Name:  "lookup",
					Input: json.RawMessage(`{"id":42}`),
				},
			},
		},
	}

	result := MapMessages(messages, "")

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	msg := result[0]
	if msg.Content != "Part 1. Part 2." {
		t.Errorf("expected concatenated text 'Part 1. Part 2.', got %q", msg.Content)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected first tool call name 'search', got %q", msg.ToolCalls[0].Function.Name)
	}
	if msg.ToolCalls[1].Function.Name != "lookup" {
		t.Errorf("expected second tool call name 'lookup', got %q", msg.ToolCalls[1].Function.Name)
	}
}

func TestMapMessages_EmptyMessages(t *testing.T) {
	result := MapMessages(nil, "")
	if len(result) != 0 {
		t.Errorf("expected 0 messages for nil input, got %d", len(result))
	}

	result = MapMessages([]bond.Message{}, "")
	if len(result) != 0 {
		t.Errorf("expected 0 messages for empty input, got %d", len(result))
	}
}

// --------------------------------------------------------------------------
// Tests for MapTools
// Validates: 3.1, 3.2, 3.3
// --------------------------------------------------------------------------

func TestMapTools_Empty(t *testing.T) {
	result, err := MapTools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	result, err = MapTools([]bond.Tool{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestMapTools_WithSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)
	tools := []bond.Tool{
		mockTool{
			name:        "get_weather",
			description: "Get the current weather",
			schema:      schema,
		},
	}

	result, err := MapTools(tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	tool := result[0]
	if tool.Type != "function" {
		t.Errorf("expected type 'function', got %q", tool.Type)
	}
	if tool.Function.Name != "get_weather" {
		t.Errorf("expected name 'get_weather', got %q", tool.Function.Name)
	}
	if tool.Function.Description != "Get the current weather" {
		t.Errorf("expected description 'Get the current weather', got %q", tool.Function.Description)
	}

	// Parameters should be the marshaled schema (json.RawMessage marshals to itself)
	var params map[string]any
	if err := json.Unmarshal(tool.Function.Parameters, &params); err != nil {
		t.Fatalf("failed to unmarshal parameters: %v", err)
	}
	if params["type"] != "object" {
		t.Errorf("expected parameters type 'object', got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to be a map")
	}
	if _, ok := props["city"]; !ok {
		t.Error("expected properties to contain 'city'")
	}
}

// --------------------------------------------------------------------------
// Tests for StreamReader
// Validates: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 6.3, 6.4
// --------------------------------------------------------------------------

func TestStreamReader_TextOnly(t *testing.T) {
	body := buildSSEResponse(
		ChatCompletionChunk{
			ID: "1",
			Choices: []ChunkChoice{
				{Index: 0, Delta: ChunkDelta{Content: "Hello"}, FinishReason: nil},
			},
		},
		ChatCompletionChunk{
			ID: "2",
			Choices: []ChunkChoice{
				{Index: 0, Delta: ChunkDelta{Content: " world"}, FinishReason: nil},
			},
		},
	)

	var events []bond.StreamEvent
	ctx := context.Background()
	StreamReader(ctx, body, "test:", func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (start, 2 text, stop), got %d", len(events))
	}
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("expected first event to be Start, got %v", events[0].Type)
	}
	if events[1].Type != bond.StreamEventTextDelta || events[1].TextDelta != "Hello" {
		t.Errorf("expected TextDelta 'Hello', got %v %q", events[1].Type, events[1].TextDelta)
	}
	if events[2].Type != bond.StreamEventTextDelta || events[2].TextDelta != " world" {
		t.Errorf("expected TextDelta ' world', got %v %q", events[2].Type, events[2].TextDelta)
	}
	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("expected last event to be Stop, got %v", last.Type)
	}
	if last.StopReason != bond.StopReasonEnd {
		t.Errorf("expected StopReason End, got %v", last.StopReason)
	}
}

func TestStreamReader_ToolCallDeltas(t *testing.T) {
	finishReason := "tool_calls"
	body := buildSSEResponse(
		ChatCompletionChunk{
			ID: "1",
			Choices: []ChunkChoice{
				{
					Index: 0,
					Delta: ChunkDelta{
						ToolCalls: []ChunkToolCallDelta{
							{Index: 0, ID: "call_1", Type: "function", Function: ChunkFunctionDelta{Name: "get_weather"}},
						},
					},
				},
			},
		},
		ChatCompletionChunk{
			ID: "2",
			Choices: []ChunkChoice{
				{
					Index: 0,
					Delta: ChunkDelta{
						ToolCalls: []ChunkToolCallDelta{
							{Index: 0, Function: ChunkFunctionDelta{Arguments: `{"city":`}},
						},
					},
				},
			},
		},
		ChatCompletionChunk{
			ID: "3",
			Choices: []ChunkChoice{
				{
					Index:        0,
					Delta:        ChunkDelta{ToolCalls: []ChunkToolCallDelta{{Index: 0, Function: ChunkFunctionDelta{Arguments: `"NYC"}`}}}},
					FinishReason: &finishReason,
				},
			},
		},
	)

	var events []bond.StreamEvent
	ctx := context.Background()
	StreamReader(ctx, body, "test:", func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	// Expect: Start, ToolUse, Stop
	var toolUseEvents []bond.StreamEvent
	var stopEvent bond.StreamEvent
	for _, ev := range events {
		switch ev.Type {
		case bond.StreamEventToolUse:
			toolUseEvents = append(toolUseEvents, ev)
		case bond.StreamEventStop:
			stopEvent = ev
		}
	}

	if len(toolUseEvents) != 1 {
		t.Fatalf("expected 1 ToolUse event, got %d", len(toolUseEvents))
	}
	tu := toolUseEvents[0]
	if tu.ToolUse.ID != "call_1" {
		t.Errorf("expected tool use ID 'call_1', got %q", tu.ToolUse.ID)
	}
	if tu.ToolUse.Name != "get_weather" {
		t.Errorf("expected tool use name 'get_weather', got %q", tu.ToolUse.Name)
	}
	expectedArgs := `{"city":"NYC"}`
	if string(tu.ToolUse.Input) != expectedArgs {
		t.Errorf("expected tool use input %q, got %q", expectedArgs, string(tu.ToolUse.Input))
	}
	if stopEvent.StopReason != bond.StopReasonToolUse {
		t.Errorf("expected StopReason ToolUse, got %v", stopEvent.StopReason)
	}
}

func TestStreamReader_DoneSentinel(t *testing.T) {
	// SSE stream with only [DONE], no chunks with finish_reason
	body := io.NopCloser(strings.NewReader("data: [DONE]\n\n"))

	var events []bond.StreamEvent
	ctx := context.Background()
	StreamReader(ctx, body, "test:", func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	if len(events) != 2 {
		t.Fatalf("expected 2 events (Start, Stop), got %d", len(events))
	}
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("expected Start event, got %v", events[0].Type)
	}
	if events[1].Type != bond.StreamEventStop {
		t.Errorf("expected Stop event, got %v", events[1].Type)
	}
	if events[1].StopReason != bond.StopReasonEnd {
		t.Errorf("expected StopReason End for [DONE], got %v", events[1].StopReason)
	}
}

func TestStreamReader_MalformedJSON(t *testing.T) {
	body := io.NopCloser(strings.NewReader("data: {invalid json\n\n"))

	var gotError error
	ctx := context.Background()
	StreamReader(ctx, body, "test:", func(event bond.StreamEvent, err error) bool {
		if err != nil {
			gotError = err
			return false
		}
		return true
	})

	if gotError == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	if !strings.Contains(gotError.Error(), "test:") {
		t.Errorf("expected error to contain prefix 'test:', got %q", gotError.Error())
	}
	if !strings.Contains(gotError.Error(), "unmarshal chunk") {
		t.Errorf("expected error to contain 'unmarshal chunk', got %q", gotError.Error())
	}
}

func TestStreamReader_ContextCancelled(t *testing.T) {
	// Create a body that has data after the context is cancelled.
	// We cancel context before StreamReader starts scanning real data.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	body := buildSSEResponse(
		ChatCompletionChunk{
			ID: "1",
			Choices: []ChunkChoice{
				{Index: 0, Delta: ChunkDelta{Content: "should not appear"}},
			},
		},
	)

	var gotError error
	StreamReader(ctx, body, "test:", func(event bond.StreamEvent, err error) bool {
		if err != nil {
			gotError = err
			return false
		}
		// Allow start event through, but the scan loop should detect ctx cancelled
		return true
	})

	if gotError == nil {
		t.Fatal("expected context cancelled error")
	}
	if !strings.Contains(gotError.Error(), "test:") {
		t.Errorf("expected error to contain prefix 'test:', got %q", gotError.Error())
	}
	if !strings.Contains(gotError.Error(), "context canceled") {
		t.Errorf("expected error to contain 'context canceled', got %q", gotError.Error())
	}
}

func TestStreamReader_FinishReasonMapping(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
		wantStop     bond.StopReason
	}{
		{name: "stop maps to End", finishReason: "stop", wantStop: bond.StopReasonEnd},
		{name: "tool_calls maps to ToolUse", finishReason: "tool_calls", wantStop: bond.StopReasonToolUse},
		{name: "length maps to Length", finishReason: "length", wantStop: bond.StopReasonLength},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := tt.finishReason
			body := buildSSEResponse(
				ChatCompletionChunk{
					ID: "1",
					Choices: []ChunkChoice{
						{
							Index:        0,
							Delta:        ChunkDelta{Content: "hi"},
							FinishReason: &fr,
						},
					},
				},
			)

			var stopEvent bond.StreamEvent
			ctx := context.Background()
			StreamReader(ctx, body, "test:", func(event bond.StreamEvent, err error) bool {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if event.Type == bond.StreamEventStop {
					stopEvent = event
				}
				return true
			})

			if stopEvent.StopReason != tt.wantStop {
				t.Errorf("expected StopReason %v, got %v", tt.wantStop, stopEvent.StopReason)
			}
		})
	}
}

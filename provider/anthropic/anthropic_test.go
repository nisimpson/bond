package anthropic_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/anthropic"
)

func TestNew_Defaults(t *testing.T) {
	agent := anthropic.New(anthropic.AgentOptions{})

	// Verify defaults by attempting a stream against a test server.
	// We check indirectly: New should not panic, and Stream should hit the default base URL.
	// For a direct check, we use a test server and confirm the agent uses the provided base URL.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The default agent should hit /v1/messages
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
	}))
	defer ts.Close()

	// Create agent with defaults but override BaseURL to test server
	defaultAgent := anthropic.New(anthropic.AgentOptions{BaseURL: ts.URL})
	ctx := context.Background()
	for _, err := range defaultAgent.Stream(ctx, []bond.Message{{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hi"}}}}) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Verify that New with empty opts doesn't panic and returns non-nil
	if agent == nil {
		t.Fatal("New returned nil")
	}
}

func TestNew_CustomValues(t *testing.T) {
	temp := 0.7
	topP := 0.9
	agent := anthropic.New(anthropic.AgentOptions{
		Model:       "claude-sonnet-4-20250514",
		BaseURL:     "https://custom.api.example.com",
		System:      "You are a helpful assistant.",
		APIKey:      "sk-test-key",
		HTTPClient:  &http.Client{},
		Temperature: &temp,
		MaxTokens:   8192,
		TopP:        &topP,
	})

	if agent == nil {
		t.Fatal("New returned nil with custom values")
	}
}

func TestStream_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal server error occurred")
	}))
	defer ts.Close()

	agent := anthropic.New(anthropic.AgentOptions{
		Model:   "claude-sonnet-4-20250514",
		BaseURL: ts.URL,
		APIKey:  "sk-test",
	})

	ctx := context.Background()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
	}

	var gotErr error
	for _, err := range agent.Stream(ctx, messages) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for HTTP 500 response")
	}

	errMsg := gotErr.Error()
	if !strings.Contains(errMsg, "500") {
		t.Errorf("error should contain status code 500, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "internal server error") {
		t.Errorf("error should contain body excerpt, got: %s", errMsg)
	}
}

func TestStream_NonStreamingError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer ts.Close()

	agent := anthropic.New(anthropic.AgentOptions{
		Model:   "claude-sonnet-4-20250514",
		BaseURL: ts.URL,
		APIKey:  "sk-invalid",
	})

	ctx := context.Background()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
	}

	var gotErr error
	for _, err := range agent.Stream(ctx, messages) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for HTTP 401 response")
	}

	errMsg := gotErr.Error()
	if !strings.Contains(errMsg, "anthropic: HTTP 401:") {
		t.Errorf("error should match format 'anthropic: HTTP 401: ...', got: %s", errMsg)
	}
}

func TestStream_SuccessfulTextStream(t *testing.T) {
	sseResponse := strings.Join([]string{
		"event: message_start",
		`data: {"id":"msg_123","type":"message","role":"assistant"}`,
		"",
		"event: content_block_start",
		`data: {"index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"text_delta","text":" world"}}`,
		"",
		"event: content_block_stop",
		`data: {"index":0}`,
		"",
		"event: message_delta",
		`data: {"delta":{"stop_reason":"end_turn"}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse)
	}))
	defer ts.Close()

	agent := anthropic.New(anthropic.AgentOptions{
		Model:   "claude-sonnet-4-20250514",
		BaseURL: ts.URL,
		APIKey:  "sk-test",
	})

	ctx := context.Background()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "say hello"}}},
	}

	var events []bond.StreamEvent
	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected stream events, got none")
	}

	// First event should be StreamEventStart
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, bond.StreamEventStart)
	}

	// Collect text deltas
	var text string
	for _, e := range events {
		if e.Type == bond.StreamEventTextDelta {
			text += e.TextDelta
		}
	}
	if text != "Hello world" {
		t.Errorf("concatenated text = %q, want %q", text, "Hello world")
	}

	// Last event should be StreamEventStop
	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("last event type = %q, want %q", last.Type, bond.StreamEventStop)
	}
	if last.StopReason != bond.StopReasonEnd {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonEnd)
	}
}

func TestStream_ToolUseResponse(t *testing.T) {
	sseResponse := strings.Join([]string{
		"event: message_start",
		`data: {"id":"msg_456","type":"message","role":"assistant"}`,
		"",
		"event: content_block_start",
		`data: {"index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"text_delta","text":"Let me check"}}`,
		"",
		"event: content_block_stop",
		`data: {"index":0}`,
		"",
		"event: content_block_start",
		`data: {"index":1,"content_block":{"type":"tool_use","id":"tool_abc","name":"get_weather"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"\"NYC\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"index":1}`,
		"",
		"event: message_delta",
		`data: {"delta":{"stop_reason":"tool_use"}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse)
	}))
	defer ts.Close()

	agent := anthropic.New(anthropic.AgentOptions{
		Model:   "claude-sonnet-4-20250514",
		BaseURL: ts.URL,
		APIKey:  "sk-test",
	})

	ctx := context.Background()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "what's the weather?"}}},
	}

	var events []bond.StreamEvent
	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	// Verify we got start, text delta, tool use, and stop events
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, bond.StreamEventStart)
	}

	// Find text delta
	var text string
	for _, e := range events {
		if e.Type == bond.StreamEventTextDelta {
			text += e.TextDelta
		}
	}
	if text != "Let me check" {
		t.Errorf("text = %q, want %q", text, "Let me check")
	}

	// Find tool use event
	var toolEvent *bond.StreamEvent
	for i := range events {
		if events[i].Type == bond.StreamEventToolUse {
			toolEvent = &events[i]
			break
		}
	}
	if toolEvent == nil {
		t.Fatal("expected StreamEventToolUse event")
	}
	if toolEvent.ToolUse.ID != "tool_abc" {
		t.Errorf("tool ID = %q, want %q", toolEvent.ToolUse.ID, "tool_abc")
	}
	if toolEvent.ToolUse.Name != "get_weather" {
		t.Errorf("tool name = %q, want %q", toolEvent.ToolUse.Name, "get_weather")
	}
	if string(toolEvent.ToolUse.Input) != `{"city":"NYC"}` {
		t.Errorf("tool input = %s, want %s", toolEvent.ToolUse.Input, `{"city":"NYC"}`)
	}

	// Last event should be stop with tool_use reason
	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("last event type = %q, want %q", last.Type, bond.StreamEventStop)
	}
	if last.StopReason != bond.StopReasonToolUse {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonToolUse)
	}
}

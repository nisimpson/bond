package anthropic_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/internal/anthropic"
)

func TestStreamReader_TextDeltas(t *testing.T) {
	sse := strings.Join([]string{
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

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	anthropic.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	// First event should be start
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
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
}

func TestStreamReader_ToolUse(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start",
		`data: {"index":0,"content_block":{"type":"tool_use","id":"tool_abc","name":"get_weather"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"input_json_delta","partial_json":"\"NYC\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"index":0}`,
		"",
		"event: message_delta",
		`data: {"delta":{"stop_reason":"tool_use"}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	anthropic.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

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
	if toolEvent.ToolUse == nil {
		t.Fatal("ToolUse field is nil")
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
}

func TestStreamReader_StopReason_EndTurn(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_delta",
		`data: {"delta":{"stop_reason":"end_turn"}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	anthropic.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	// Last event should be stop with StopReasonEnd
	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("last event type = %q, want %q", last.Type, bond.StreamEventStop)
	}
	if last.StopReason != bond.StopReasonEnd {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonEnd)
	}
}

func TestStreamReader_StopReason_ToolUse(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_delta",
		`data: {"delta":{"stop_reason":"tool_use"}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	anthropic.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("last event type = %q, want %q", last.Type, bond.StreamEventStop)
	}
	if last.StopReason != bond.StopReasonToolUse {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonToolUse)
	}
}

func TestStreamReader_StopReason_MaxTokens(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_delta",
		`data: {"delta":{"stop_reason":"max_tokens"}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	anthropic.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("last event type = %q, want %q", last.Type, bond.StreamEventStop)
	}
	if last.StopReason != bond.StopReasonLength {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonLength)
	}
}

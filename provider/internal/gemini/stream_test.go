package gemini_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/internal/gemini"
)

func TestStreamReader_TextParts(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]},"finishReason":"STOP"}]}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	gemini.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	if events[0].Type != bond.StreamEventStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, bond.StreamEventStart)
	}

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

func TestStreamReader_FunctionCall(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"NYC"}}}]},"finishReason":"STOP"}]}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	gemini.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

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
	if toolEvent.ToolUse.ID == "" {
		t.Error("tool ID is empty, expected a non-empty UUID")
	}
	if toolEvent.ToolUse.Name != "get_weather" {
		t.Errorf("tool name = %q, want %q", toolEvent.ToolUse.Name, "get_weather")
	}
	if string(toolEvent.ToolUse.Input) != `{"city":"NYC"}` {
		t.Errorf("tool input = %s, want %s", toolEvent.ToolUse.Input, `{"city":"NYC"}`)
	}
}

func TestStreamReader_FinishReason_STOP(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	gemini.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
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
	if last.StopReason != bond.StopReasonEnd {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonEnd)
	}
}

func TestStreamReader_FinishReason_MAX_TOKENS(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"truncated"}]},"finishReason":"MAX_TOKENS"}]}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	gemini.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
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

func TestStreamReader_MultipleParts(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"},{"text":" world"},{"functionCall":{"name":"greet","args":{"name":"Alice"}}}]},"finishReason":"STOP"}]}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sse))
	ctx := context.Background()

	var events []bond.StreamEvent
	gemini.StreamReader(ctx, body, func(event bond.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
		return true
	})

	// Expect: Start, TextDelta("Hello"), TextDelta(" world"), ToolUse, Stop
	var textDeltas int
	var toolUses int
	var text string
	for _, e := range events {
		switch e.Type {
		case bond.StreamEventTextDelta:
			textDeltas++
			text += e.TextDelta
		case bond.StreamEventToolUse:
			toolUses++
			if e.ToolUse.Name != "greet" {
				t.Errorf("tool name = %q, want %q", e.ToolUse.Name, "greet")
			}
		}
	}

	if textDeltas != 2 {
		t.Errorf("text delta count = %d, want 2", textDeltas)
	}
	if text != "Hello world" {
		t.Errorf("combined text = %q, want %q", text, "Hello world")
	}
	if toolUses != 1 {
		t.Errorf("tool use count = %d, want 1", toolUses)
	}
}

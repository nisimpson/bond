package bond_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

func TestStream_TextResponse(t *testing.T) {
	agent := &bondtest.Agent{Events: bondtest.TextEvents("Hello, ", "world!")}

	var collected string
	for event, err := range bond.Stream(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{}) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.Type == bond.StreamEventTextDelta {
			collected += event.TextDelta
		}
	}

	if collected != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", collected)
	}
}

func TestInvoke_TextResponse(t *testing.T) {
	agent := &bondtest.Agent{Events: bondtest.TextEvents("test response")}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "test response" {
		t.Errorf("expected 'test response', got %q", resp.Text)
	}
	if resp.StopReason != bond.StopReasonEnd {
		t.Errorf("expected StopReasonEnd, got %v", resp.StopReason)
	}
}

func TestInvoke_ErrorPropagation(t *testing.T) {
	agent := &bondtest.Agent{Err: errors.New("model failed")}

	_, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "model failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStream_ToolExecution(t *testing.T) {
	callCount := 0
	tool := &fakeTool{
		name: "greet",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			callCount++
			return []bond.Block{&bond.TextBlock{Text: "hello from tool"}}, nil
		},
	}

	// Agent requests tool on first call, then responds with text on second.
	callNum := 0
	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID: "1", Name: "greet", Input: json.RawMessage(`{}`),
			}),
			bondtest.TextEvents("done"),
		),
	}
	_ = callNum

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Tools: []bond.Tool{tool},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected tool called once, got %d", callCount)
	}
	if resp.Text != "done" {
		t.Errorf("expected 'done', got %q", resp.Text)
	}
}

func TestStream_MaxTurns(t *testing.T) {
	// Agent always requests a tool — should be limited by MaxTurns.
	agent := &bondtest.Agent{
		StreamFunc: bondtest.Repeat(bondtest.ToolUseEvents(&bond.ToolUseBlock{
			ID: "1", Name: "loop", Input: json.RawMessage(`{}`),
		})),
	}

	tool := &fakeTool{
		name: "loop",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			return []bond.Block{&bond.TextBlock{Text: "ok"}}, nil
		},
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("go"), bond.AgentOptions{
		Tools:    []bond.Tool{tool},
		MaxTurns: 3,
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// Should have stopped after 3 turns without a final text response.
	_ = resp
}

func TestStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	agent := &bondtest.Agent{Events: bondtest.TextEvents("should not get here")}

	_, err := bond.Invoke(ctx, agent, bond.TextPrompt("hi"), bond.AgentOptions{})
	if err == nil {
		t.Fatal("expected context error")
	}
}

// --- helpers ---

type fakeTool struct {
	name  string
	runFn func(context.Context, json.RawMessage) ([]bond.Block, error)
}

func (t *fakeTool) Name() string              { return t.name }
func (t *fakeTool) Description() string       { return t.name }
func (t *fakeTool) InputSchema() json.Marshaler { return json.RawMessage(`{"type":"object"}`) }
func (t *fakeTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	if t.runFn != nil {
		return t.runFn(ctx, input)
	}
	return []bond.Block{&bond.TextBlock{Text: "ok"}}, nil
}

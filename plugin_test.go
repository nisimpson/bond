package bond_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

func TestNewToolsPlugin(t *testing.T) {
	tool := &fakeTool{name: "gadget"}
	plugin := bond.NewToolsPlugin("q-branch", tool)

	if plugin.Name() != "q-branch" {
		t.Errorf("expected 'q-branch', got %q", plugin.Name())
	}
	if len(plugin.Tools()) != 1 {
		t.Errorf("expected 1 tool, got %d", len(plugin.Tools()))
	}
}

func TestNewHooksPlugin(t *testing.T) {
	var initCalled bool
	plugin := bond.NewHooksPlugin("observer", func(r *bond.HookRegistry) {
		initCalled = true
	})

	if plugin.Name() != "observer" {
		t.Errorf("expected 'observer', got %q", plugin.Name())
	}
	if len(plugin.Tools()) != 0 {
		t.Errorf("expected 0 tools, got %d", len(plugin.Tools()))
	}

	plugin.Init(&bond.HookRegistry{})
	if !initCalled {
		t.Error("Init was not called")
	}
}

func TestStream_PluginToolsInjected(t *testing.T) {
	// Plugin provides a tool that the agent will call.
	callCount := 0
	plugin := bond.NewToolsPlugin("gadgets", &fakeTool{
		name: "pen_explosive",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			callCount++
			return []bond.Block{&bond.TextBlock{Text: "boom"}}, nil
		},
	})

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID: "1", Name: "pen_explosive", Input: json.RawMessage(`{}`),
			}),
			bondtest.TextEvents("mission complete"),
		),
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("go"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected tool called once, got %d", callCount)
	}
	if resp.Text != "mission complete" {
		t.Errorf("expected 'mission complete', got %q", resp.Text)
	}
}

func TestStream_PluginHooksFire(t *testing.T) {
	var beforeStream, afterStream bool

	plugin := bond.NewHooksPlugin("spy", func(r *bond.HookRegistry) {
		bond.OnBefore(r, func(ctx context.Context, e *bond.BeforeStreamHook) error {
			beforeStream = true
			return nil
		})
		bond.OnAfter(r, func(ctx context.Context, e *bond.AfterStreamHook) {
			afterStream = true
		})
	})

	agent := &bondtest.Agent{Events: bondtest.TextEvents("hello")}

	_, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !beforeStream {
		t.Error("BeforeStreamHook was not fired")
	}
	if !afterStream {
		t.Error("AfterStreamHook was not fired")
	}
}

func TestStream_BeforeStreamHookAbort(t *testing.T) {
	plugin := bond.NewHooksPlugin("gatekeeper", func(r *bond.HookRegistry) {
		bond.OnBefore(r, func(ctx context.Context, e *bond.BeforeStreamHook) error {
			return bond.ErrAbort
		})
	})

	agent := &bondtest.Agent{Events: bondtest.TextEvents("should not reach")}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("expected no error (abort is silent), got %v", err)
	}
	if resp.Text != "" {
		t.Errorf("expected empty response on abort, got %q", resp.Text)
	}
}

func TestStream_BeforeToolCallHookAbort(t *testing.T) {
	plugin := bond.NewHooksPlugin("blocker", func(r *bond.HookRegistry) {
		bond.OnBefore(r, func(ctx context.Context, e *bond.BeforeToolCallHook) error {
			return bond.ErrAbort
		})
	})

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID: "1", Name: "blocked_tool", Input: json.RawMessage(`{}`),
			}),
			bondtest.TextEvents("continued after abort"),
		),
	}

	tool := &fakeTool{
		name: "blocked_tool",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			t.Error("tool should not have been called")
			return nil, nil
		},
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("go"), bond.AgentOptions{
		Tools:   []bond.Tool{tool},
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "continued after abort" {
		t.Errorf("expected 'continued after abort', got %q", resp.Text)
	}
}

func TestStream_UnknownTool(t *testing.T) {
	// Agent requests a tool that doesn't exist.
	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID: "1", Name: "nonexistent", Input: json.RawMessage(`{}`),
			}),
			bondtest.TextEvents("handled gracefully"),
		),
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// Should have passed an error tool result back to the model.
	if resp.Text != "handled gracefully" {
		t.Errorf("expected 'handled gracefully', got %q", resp.Text)
	}
}

func TestStream_ToolError(t *testing.T) {
	tool := &fakeTool{
		name: "broken",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			return nil, errors.New("tool exploded")
		},
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID: "1", Name: "broken", Input: json.RawMessage(`{}`),
			}),
			bondtest.TextEvents("recovered"),
		),
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("go"), bond.AgentOptions{
		Tools: []bond.Tool{tool},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("expected 'recovered', got %q", resp.Text)
	}
}

func TestStream_MediaDelta(t *testing.T) {
	events := []bond.StreamEvent{
		{Type: bond.StreamEventStart},
		{Type: bond.StreamEventMediaDelta, MediaDelta: &bond.MediaDelta{
			MIMEType: "image/png",
			Data:     []byte("fake-png-data"),
		}},
		{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd},
	}

	agent := &bondtest.Agent{Events: events}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("show me"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(resp.Media) != 1 {
		t.Fatalf("expected 1 media, got %d", len(resp.Media))
	}
	if resp.Media[0].MIMEType != "image/png" {
		t.Errorf("expected image/png, got %q", resp.Media[0].MIMEType)
	}
	if string(resp.Media[0].Data) != "fake-png-data" {
		t.Errorf("unexpected data: %v", resp.Media[0].Data)
	}
}

func TestStream_ToolsFromContext(t *testing.T) {
	// Verify that tools are passed in context to the agent.
	var toolNames []string
	agent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			tools := bond.ToolsFromContext(ctx)
			for _, tool := range tools {
				toolNames = append(toolNames, tool.Name())
			}
			return func(yield func(bond.StreamEvent, error) bool) {
				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
			}
		},
	}

	_, _ = bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Tools: []bond.Tool{&fakeTool{name: "alpha"}, &fakeTool{name: "beta"}},
	})

	if len(toolNames) != 2 {
		t.Fatalf("expected 2 tools in context, got %d", len(toolNames))
	}
}

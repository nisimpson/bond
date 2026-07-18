package slogger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
	"github.com/nisimpson/bond/extra/slogger"
)

func TestPlugin_LogsStreamLifecycle(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	plugin := slogger.NewPlugin(slogger.Options{
		Logger: logger.With("request_id", "req-123"),
	})

	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hello"),
	}

	_, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should contain stream.start and stream.end
	if !strings.Contains(output, "stream.start") {
		t.Error("expected stream.start log message")
	}
	if !strings.Contains(output, "stream.end") {
		t.Error("expected stream.end log message")
	}
	// Should contain model.invoke and model.response
	if !strings.Contains(output, "model.invoke") {
		t.Error("expected model.invoke log message")
	}
	if !strings.Contains(output, "model.response") {
		t.Error("expected model.response log message")
	}
	// Should contain the request_id attribute
	if !strings.Contains(output, "req-123") {
		t.Error("expected request_id attribute in log output")
	}
}

func TestPlugin_LogsToolCalls(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	plugin := slogger.NewPlugin(slogger.Options{Logger: logger})

	adder, _ := bond.NewFuncTool(
		func(ctx context.Context, in struct{ A, B int }) (struct{ Sum int }, error) {
			return struct{ Sum int }{Sum: in.A + in.B}, nil
		},
		bond.FuncToolOptions{Name: "add", Description: "adds numbers"},
	)

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID:    "call_1",
				Name:  "add",
				Input: json.RawMessage(`{"A":1,"B":2}`),
			}),
			bondtest.TextEvents("done"),
		),
	}

	_, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("add"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
		Tools:   []bond.Tool{adder},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "tool.call.start") {
		t.Error("expected tool.call.start log message")
	}
	if !strings.Contains(output, "tool.call.end") {
		t.Error("expected tool.call.end log message")
	}
	if !strings.Contains(output, `"tool":"add"`) && !strings.Contains(output, `"tool": "add"`) {
		t.Error("expected tool name 'add' in log output")
	}
}

func TestFromContext_Default(t *testing.T) {
	// Without plugin, FromContext should return slog.Default()
	logger := slogger.FromContext(context.Background())
	if logger == nil {
		t.Fatal("FromContext returned nil")
	}
}

func TestPlugin_ImplementsContextPlugin(t *testing.T) {
	plugin := slogger.NewPlugin(slogger.Options{})

	// Verify it implements ContextPlugin
	var _ bond.ContextPlugin = plugin
}

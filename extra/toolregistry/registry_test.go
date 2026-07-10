package toolregistry_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/toolregistry"
)

func TestRegistry_ThreeTools(t *testing.T) {
	reg := toolregistry.New(toolregistry.Options{
		Tools: []bond.Tool{&fakeTool{name: "a"}, &fakeTool{name: "b"}},
	})

	tools := reg.Tools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 meta-tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	for _, expected := range []string{"list_tools", "describe_tool", "use_tool"} {
		if !names[expected] {
			t.Errorf("missing tool %q", expected)
		}
	}
}

func TestRegistry_ListTools(t *testing.T) {
	reg := toolregistry.New(toolregistry.Options{
		Tools: []bond.Tool{
			&fakeTool{name: "search", desc: "Search the web"},
			&fakeTool{name: "calc", desc: "Calculator"},
			&fakeTool{name: "db_query", desc: "Query database"},
		},
	})

	listTool := reg.Tools()[0] // list_tools

	// No filter — returns all.
	blocks, err := listTool.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := blocks[0].(*bond.TextBlock).Text
	var results []map[string]string
	_ = json.Unmarshal([]byte(text), &results)
	if len(results) != 3 {
		t.Errorf("expected 3 tools, got %d", len(results))
	}

	// With filter.
	blocks, _ = listTool.Run(context.Background(), json.RawMessage(`{"filter":"database"}`))
	text = blocks[0].(*bond.TextBlock).Text
	_ = json.Unmarshal([]byte(text), &results)
	if len(results) != 1 {
		t.Errorf("expected 1 filtered tool, got %d", len(results))
	}
	if results[0]["name"] != "db_query" {
		t.Errorf("expected db_query, got %q", results[0]["name"])
	}
}

func TestRegistry_DescribeTool(t *testing.T) {
	reg := toolregistry.New(toolregistry.Options{
		Tools: []bond.Tool{&fakeTool{name: "search", desc: "Search the web"}},
	})

	describeTool := reg.Tools()[1] // describe_tool

	blocks, err := describeTool.Run(context.Background(), json.RawMessage(`{"name":"search"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := blocks[0].(*bond.TextBlock).Text
	var desc map[string]any
	_ = json.Unmarshal([]byte(text), &desc)
	if desc["name"] != "search" {
		t.Errorf("expected name 'search', got %v", desc["name"])
	}
}

func TestRegistry_DescribeTool_NotFound(t *testing.T) {
	reg := toolregistry.New(toolregistry.Options{Tools: nil})
	describeTool := reg.Tools()[1]

	blocks, err := describeTool.Run(context.Background(), json.RawMessage(`{"name":"nope"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := blocks[0].(*bond.TextBlock).Text
	if text == "" {
		t.Error("expected not-found message")
	}
}

func TestRegistry_UseTool(t *testing.T) {
	called := false
	reg := toolregistry.New(toolregistry.Options{
		Tools: []bond.Tool{&fakeTool{
			name: "greet",
			runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
				called = true
				return []bond.Block{&bond.TextBlock{Text: "hi!"}}, nil
			},
		}},
	})

	useTool := reg.Tools()[2] // use_tool

	blocks, err := useTool.Run(context.Background(), json.RawMessage(`{"name":"greet","arguments":{"who":"world"}}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Error("expected tool to be called")
	}
	text := blocks[0].(*bond.TextBlock).Text
	if text != "hi!" {
		t.Errorf("expected 'hi!', got %q", text)
	}
}

func TestRegistry_UseTool_NotFound(t *testing.T) {
	reg := toolregistry.New(toolregistry.Options{Tools: nil})
	useTool := reg.Tools()[2]

	blocks, err := useTool.Run(context.Background(), json.RawMessage(`{"name":"nope","arguments":{}}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := blocks[0].(*bond.TextBlock).Text
	if text == "" {
		t.Error("expected not-found message")
	}
}

func TestRegistry_FilterHelp(t *testing.T) {
	reg := toolregistry.New(toolregistry.Options{
		Tools: []bond.Tool{&fakeTool{name: "a"}},
		FilterHelp: func() []toolregistry.FilterExample {
			return []toolregistry.FilterExample{
				{Filter: "cat:db", Description: "database tools"},
			}
		},
	})

	listTool := reg.Tools()[0]
	desc := listTool.Description()
	if desc == "List available tools, optionally filtered by a query string." {
		t.Error("expected augmented description with filter help")
	}
}

// --- helpers ---

type fakeTool struct {
	name  string
	desc  string
	runFn func(context.Context, json.RawMessage) ([]bond.Block, error)
}

func (t *fakeTool) Name() string                { return t.name }
func (t *fakeTool) Description() string         { return t.desc }
func (t *fakeTool) InputSchema() json.Marshaler { return json.RawMessage(`{"type":"object"}`) }
func (t *fakeTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	if t.runFn != nil {
		return t.runFn(ctx, input)
	}
	return []bond.Block{&bond.TextBlock{Text: "ok"}}, nil
}

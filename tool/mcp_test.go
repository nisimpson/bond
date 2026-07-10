package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

// mockSession implements the tool.Session interface.
type mockSession struct {
	tools   []*mcp.Tool
	listErr error
	callFn  func(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

func (m *mockSession) ListTools(_ context.Context, _ *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &mcp.ListToolsResult{Tools: m.tools}, nil
}

func (m *mockSession) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if m.callFn != nil {
		return m.callFn(ctx, params)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "default response"}},
	}, nil
}

func TestServerTools_Success(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "tool_a", Description: "Tool A"},
			{Name: "tool_b", Description: "Tool B"},
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name() != "tool_a" {
		t.Fatalf("expected 'tool_a', got %q", tools[0].Name())
	}
	if tools[1].Name() != "tool_b" {
		t.Fatalf("expected 'tool_b', got %q", tools[1].Name())
	}
}

func TestServerTools_ListError(t *testing.T) {
	session := &mockSession{
		listErr: errors.New("connection refused"),
	}

	_, err := tool.FromMCP(context.Background(), session)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected 'connection refused' in error, got %q", err.Error())
	}
}

func TestServerTools_EmptyList(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestMCPTool_Description(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "calculator", Description: "does math"},
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].Description() != "does math" {
		t.Fatalf("expected 'does math', got %q", tools[0].Description())
	}
}

func TestMCPTool_InputSchema(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "test", Description: "test tool"},
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	schema := tools[0].InputSchema()
	if schema == nil {
		t.Fatal("expected non-nil InputSchema")
	}

	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("failed to marshal InputSchema: %v", err)
	}
	// Should produce valid JSON
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("InputSchema not valid JSON: %v", err)
	}
}

func TestMCPTool_Run_Success(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "greet", Description: "greets"},
		},
		callFn: func(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
			if params.Name != "greet" {
				t.Fatalf("expected tool name 'greet', got %q", params.Name)
			}
			args, _ := params.Arguments.(map[string]any)
			name, _ := args["name"].(string)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Hello, " + name}},
			}, nil
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := json.RawMessage(`{"name":"Alice"}`)
	blocks, err := tools[0].Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	if tb.Text != "Hello, Alice" {
		t.Fatalf("expected 'Hello, Alice', got %q", tb.Text)
	}
}

func TestMCPTool_Run_EmptyInput(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "ping", Description: "pings"},
		},
		callFn: func(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			}, nil
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	blocks, err := tools[0].Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tb := blocks[0].(*bond.TextBlock)
	if tb.Text != "pong" {
		t.Fatalf("expected 'pong', got %q", tb.Text)
	}
}

func TestMCPTool_Run_CallError(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "failing", Description: "fails"},
		},
		callFn: func(_ context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
			return nil, errors.New("network error")
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tools[0].Run(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Fatalf("expected 'network error' in error, got %q", err.Error())
	}
}

func TestMCPTool_Run_ToolReturnsError(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "err_tool", Description: "returns error"},
		},
		callFn: func(_ context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "something went wrong"}},
				IsError: true,
			}, nil
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tools[0].Run(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for tool error result, got nil")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Fatalf("expected 'something went wrong' in error, got %q", err.Error())
	}
}

func TestMCPTool_Run_InvalidJSON(t *testing.T) {
	session := &mockSession{
		tools: []*mcp.Tool{
			{Name: "test", Description: "test"},
		},
	}

	tools, err := tool.FromMCP(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tools[0].Run(context.Background(), json.RawMessage(`not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON input, got nil")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("expected 'unmarshal' in error, got %q", err.Error())
	}
}

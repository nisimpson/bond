// Package toolmcp adapts MCP server tools for use in bond agent loops.
// It bridges the Model Context Protocol SDK's client session to bond's
// Tool interface, allowing bond agents to invoke tools hosted on MCP servers.
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nisimpson/bond"
)

// Session defines the subset of *mcp.ClientSession used by this package.
// Allows mocking in tests.
type Session interface {
	ListTools(ctx context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

// ServerTools discovers tools from an MCP server session and returns them
// as bond Tools. Each returned tool delegates execution to the MCP server
// via CallTool.
func FromMCP(ctx context.Context, session Session) ([]bond.Tool, error) {
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("toolmcp: list tools: %w", err)
	}

	tools := make([]bond.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, &mcpTool{
			session: session,
			tool:    t,
		})
	}
	return tools, nil
}

// mcpTool wraps an MCP Tool as a bond.Tool.
type mcpTool struct {
	session Session
	tool    *mcp.Tool
}

func (t *mcpTool) Name() string        { return t.tool.Name }
func (t *mcpTool) Description() string { return t.tool.Description }

func (t *mcpTool) InputSchema() json.Marshaler {
	return mcpSchema{schema: t.tool.InputSchema}
}

func (t *mcpTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	// Unmarshal input into a map for CallTool arguments.
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("toolmcp: unmarshal arguments: %w", err)
		}
	}

	result, err := t.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.tool.Name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("toolmcp: call %q: %w", t.tool.Name, err)
	}

	blocks := resultToBlocks(result)

	if result.IsError {
		return nil, fmt.Errorf("toolmcp: tool %q returned error: %s", t.tool.Name, blocksToText(blocks))
	}

	return blocks, nil
}

// resultToBlocks converts MCP CallToolResult content to bond blocks.
func resultToBlocks(result *mcp.CallToolResult) []bond.Block {
	var blocks []bond.Block
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			blocks = append(blocks, &bond.TextBlock{Text: c.Text})
		default:
			// For non-text content, marshal to JSON as a text block.
			data, err := json.Marshal(c)
			if err == nil {
				blocks = append(blocks, &bond.TextBlock{Text: string(data)})
			}
		}
	}
	return blocks
}

// blocksToText extracts text from blocks for error messages.
func blocksToText(blocks []bond.Block) string {
	for _, b := range blocks {
		if tb, ok := b.(*bond.TextBlock); ok {
			return tb.Text
		}
	}
	return "unknown error"
}

// mcpSchema wraps an MCP tool's InputSchema as a json.Marshaler.
type mcpSchema struct {
	schema any
}

func (s mcpSchema) MarshalJSON() ([]byte, error) {
	if s.schema == nil {
		return []byte(`{"type":"object"}`), nil
	}
	return json.Marshal(s.schema)
}

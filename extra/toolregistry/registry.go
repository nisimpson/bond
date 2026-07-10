// Package toolregistry provides a plugin that exposes a large collection of
// tools through a stable 3-tool gateway: list_tools, describe_tool, use_tool.
//
// This avoids flooding the model's context with dozens of tool definitions.
// Instead, the model discovers and invokes tools on demand.
package toolregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/internal/must"
)

// Filter is a function that determines whether a tool matches a query.
// If nil, all tools match any query.
type Filter func(tool bond.Tool, query string) bool

// FilterHelp is a function that returns structured examples of how to use
// the filter. The returned string is appended to the list_tools description
// to guide the model on effective filter usage.
type FilterHelp func() []FilterExample

// FilterExample represents a single example of how to use the filter parameter
// in list_tools. It pairs a filter string with a human-readable description
// of what that filter matches.
type FilterExample struct {
	// Filter is the example query string (e.g., "category:database").
	Filter string
	// Description explains what the filter matches (e.g., "tools in the database category").
	Description string
}

func (f FilterExample) String() string {
	return fmt.Sprintf(`"%s": %s`, f.Filter, f.Description)
}

// Options configures the tool registry.
type Options struct {
	// Tools is the full collection of tools available through the registry.
	Tools []bond.Tool
	// Filter is an optional function for filtering tools by query.
	// If nil, a default filter matching on name and description is used.
	Filter Filter
	// FilterHelp provides usage examples for the filter parameter.
	// If set, the returned examples are appended to list_tools' description.
	//
	// Example:
	//
	//	FilterHelp: func() []toolregistry.FilterExample {
	//	    return []toolregistry.FilterExample{
	//	        {Filter: "category:database", Description: "tools in the database category"},
	//	        {Filter: "tag:read-only", Description: "tools tagged as read-only"},
	//	        {Filter: "sql", Description: "tools matching 'sql' in name or description"},
	//	    }
	//	},
	FilterHelp FilterHelp
}

// Plugin is a bond.Plugin that exposes a large tool collection through
// three meta-tools: list_tools, describe_tool, and use_tool.
//
// Example:
//
//	registry := toolregistry.New(toolregistry.Options{
//	    Tools: allMyTools,
//	})
//
//	bond.Stream(ctx, agent, msgs, bond.AgentOptions{
//	    Plugins: []bond.Plugin{registry},
//	})
type Plugin struct {
	tools      map[string]bond.Tool
	list       []bond.Tool
	filter     Filter
	filterHelp FilterHelp
}

// New creates a tool registry plugin.
func New(opts Options) *Plugin {
	m := make(map[string]bond.Tool, len(opts.Tools))
	for _, t := range opts.Tools {
		m[t.Name()] = t
	}

	filter := opts.Filter
	if filter == nil {
		filter = defaultFilter
	}

	return &Plugin{
		tools:      m,
		list:       opts.Tools,
		filter:     filter,
		filterHelp: opts.FilterHelp,
	}
}

func (r *Plugin) Name() string                     { return "toolregistry" }
func (r *Plugin) Init(registry *bond.HookRegistry) {}

// Tools returns the three meta-tools that the agent sees.
func (r *Plugin) Tools() []bond.Tool {
	return []bond.Tool{
		&listTool{registry: r},
		&describeTool{registry: r},
		&useTool{registry: r},
	}
}

// defaultFilter matches tools by name or description substring (case-insensitive).
func defaultFilter(tool bond.Tool, query string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(tool.Name()), q) ||
		strings.Contains(strings.ToLower(tool.Description()), q)
}

// --- list_tools ---

type listTool struct {
	registry *Plugin
}

func (t *listTool) Name() string { return "list_tools" }
func (t *listTool) Description() string {
	base := "List available tools, optionally filtered by a query string."
	if t.registry.filterHelp != nil {
		builder := &strings.Builder{}
		_ = must.Return(builder.WriteString("\n\nFilter examples:\n"))
		for _, example := range t.registry.filterHelp() {
			_ = must.Return(fmt.Fprintf(builder, `- %s\n`, example))
		}
		base = base + builder.String()
	}
	return base
}
func (t *listTool) InputSchema() json.Marshaler {
	return jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"filter": {
				Type:        "string",
				Description: "Optional filter query to match tools by name or description.",
			},
		},
	}
}

func (t *listTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	var params struct {
		Filter string `json:"filter"`
	}
	if len(input) > 0 {
		_ = json.Unmarshal(input, &params)
	}

	type toolEntry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	var results []toolEntry
	for _, tool := range t.registry.list {
		if t.registry.filter(tool, params.Filter) {
			results = append(results, toolEntry{
				Name:        tool.Name(),
				Description: tool.Description(),
			})
		}
	}

	data, _ := json.Marshal(results)
	return []bond.Block{&bond.TextBlock{Text: string(data)}}, nil
}

// --- describe_tool ---

type describeTool struct {
	registry *Plugin
}

func (t *describeTool) Name() string { return "describe_tool" }
func (t *describeTool) Description() string {
	return "Get the full description and input schema for a specific tool."
}
func (t *describeTool) InputSchema() json.Marshaler {
	return jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {
				Type:        "string",
				Description: "The name of the tool to describe.",
			},
		},
		Required: []string{"name"},
	}
}

func (t *describeTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("describe_tool: %w", err)
	}

	tool, exists := t.registry.tools[params.Name]
	if !exists {
		return []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("tool %q not found", params.Name)}}, nil
	}

	type toolDesc struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}

	var schema json.RawMessage
	if s := tool.InputSchema(); s != nil {
		schema, _ = s.MarshalJSON()
	}

	desc := toolDesc{
		Name:        tool.Name(),
		Description: tool.Description(),
		InputSchema: schema,
	}

	data, _ := json.Marshal(desc)
	return []bond.Block{&bond.TextBlock{Text: string(data)}}, nil
}

// --- use_tool ---

type useTool struct {
	registry *Plugin
}

func (t *useTool) Name() string        { return "use_tool" }
func (t *useTool) Description() string { return "Execute a tool by name with the given arguments." }
func (t *useTool) InputSchema() json.Marshaler {
	return jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {
				Type:        "string",
				Description: "The name of the tool to execute.",
			},
			"arguments": {
				Type:        "object",
				Description: "The arguments to pass to the tool.",
			},
		},
		Required: []string{"name", "arguments"},
	}
}

func (t *useTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("use_tool: %w", err)
	}

	tool, exists := t.registry.tools[params.Name]
	if !exists {
		return []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("tool %q not found", params.Name)}}, nil
	}

	return tool.Run(ctx, params.Arguments)
}

// Verify interface compliance.
var _ bond.Plugin = (*Plugin)(nil)

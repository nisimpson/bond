package bond

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nisimpson/bond/internal/must"
	"github.com/nisimpson/bond/internal/validate"
)

// Tool represents a tool the agent can invoke during streaming.
type Tool interface {
	// Name is the tool identifier the model will reference.
	Name() string
	// Description helps the model decide when to use this tool.
	Description() string
	// InputSchema is the JSON schema describing expected input parameters.
	InputSchema() json.Marshaler
	// Run executes the tool and returns result blocks.
	Run(ctx context.Context, input json.RawMessage) ([]Block, error)
}

type FuncToolOptions struct {
	Name         string
	Description  string
	InputSchema  json.Marshaler
	OutputSchema json.Marshaler
}

func (f *FuncToolOptions) isValid() validate.Rule {
	if f.InputSchema == nil || f.OutputSchema == nil {
		defaultSchema := must.Return(json.Marshal(map[string]any{"type": "object"}))
		if f.InputSchema == nil {
			f.InputSchema = json.RawMessage(defaultSchema)
		}
		if f.OutputSchema == nil {
			f.OutputSchema = json.RawMessage(defaultSchema)
		}
	}

	return validate.Group("func tool options",
		validate.That(f.Name != "", "name is required"),
		validate.That(f.Description != "", "description is required"),
	)
}

// FuncTool is a Tool implementation backed by a function. The zero value is
// not usable; create instances with [NewFuncTool].
type FuncTool struct {
	options FuncToolOptions
	handler func(context.Context, json.RawMessage) ([]Block, error)
}

func (ft FuncTool) Name() string                 { return ft.options.Name }
func (ft FuncTool) Description() string          { return ft.options.Description }
func (ft FuncTool) InputSchema() json.Marshaler  { return ft.options.InputSchema }
func (ft FuncTool) OutputSchema() json.Marshaler { return ft.options.OutputSchema }

func (ft FuncTool) Run(ctx context.Context, input json.RawMessage) ([]Block, error) {
	return ft.handler(ctx, input)
}

// NewFuncTool creates a [Tool] backed by a typed function. The function's input
// is unmarshaled from JSON, and its output is marshaled back into a TextBlock.
// Returns an error if required options (Name, Description) or the handler are missing.
func NewFuncTool[In, Out any](fn func(context.Context, In) (Out, error), options FuncToolOptions) (Tool, error) {
	err := validate.All(
		options.isValid(),
		validate.That(fn != nil, "handler function is required"),
	)
	if err != nil {
		return nil, err
	}

	handler := func(ctx context.Context, raw json.RawMessage) ([]Block, error) {
		var input In
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, fmt.Errorf("unmarshal input: %w", err)
			}
		}

		output, err := fn(ctx, input)
		if err != nil {
			return nil, err
		}

		data, err := json.Marshal(output)
		if err != nil {
			return nil, fmt.Errorf("marshal output: %w", err)
		}

		return []Block{&TextBlock{Text: string(data)}}, nil
	}

	return &FuncTool{options: options, handler: handler}, nil
}

// ToolConfirmationProvider decides whether a tool call should proceed.
// Implementations may prompt a user, check a policy, or consult an external
// service. The provider receives the full [ToolUseBlock] (tool name and input)
// and returns whether to allow or deny execution.
type ToolConfirmationProvider interface {
	// ConfirmToolUse is called before each tool invocation. Return true to
	// allow the call, or false to deny it. A denied call aborts with
	// [ErrAbort] and surfaces an error result to the model.
	ConfirmToolUse(ctx context.Context, toolUse *ToolUseBlock) (bool, error)
}

// ToolConfirmationFunc adapts a plain function into a [ToolConfirmationProvider].
type ToolConfirmationFunc func(ctx context.Context, toolUse *ToolUseBlock) (bool, error)

// ConfirmToolUse implements [ToolConfirmationProvider].
func (f ToolConfirmationFunc) ConfirmToolUse(ctx context.Context, toolUse *ToolUseBlock) (bool, error) {
	return f(ctx, toolUse)
}

// NewToolConfirmationPlugin returns a [Plugin] that intercepts [BeforeToolCallHook]
// and invokes the given provider before each tool execution. If the provider
// denies the call, the hook returns [ErrAbort] which causes the agent loop to
// skip the tool and return an error result to the model.
//
// The provider is responsible for deciding which tools require confirmation.
// For example, it may allow all read-only tools unconditionally and only prompt
// for write operations.
func NewToolConfirmationPlugin(provider ToolConfirmationProvider) Plugin {
	return NewHooksPlugin("tool_confirmation", func(registry *HookRegistry) {
		OnBefore(registry, BeforeHookFunc[*BeforeToolCallHook](func(ctx context.Context, event *BeforeToolCallHook) error {
			allowed, err := provider.ConfirmToolUse(ctx, event.ToolUse)
			if err != nil {
				return fmt.Errorf("tool confirmation failed: %w", err)
			}
			if !allowed {
				return fmt.Errorf("tool call %q denied by confirmation provider: %w", event.ToolUse.Name, ErrAbort)
			}
			return nil
		}))
	})
}

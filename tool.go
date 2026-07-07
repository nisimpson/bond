package helix

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nisimpson/helix/internal/must"
	"github.com/nisimpson/helix/internal/validate"
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

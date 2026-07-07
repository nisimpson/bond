package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nisimpson/helix"
)

// structuredTool wraps a Tool and validates its output against a JSON Schema.
type structuredTool struct {
	inner        helix.Tool
	outputSchema Schema
}

func (t *structuredTool) Name() string                { return t.inner.Name() }
func (t *structuredTool) Description() string         { return t.inner.Description() }
func (t *structuredTool) InputSchema() json.Marshaler { return t.inner.InputSchema() }

func (t *structuredTool) Run(ctx context.Context, input json.RawMessage) ([]helix.Block, error) {
	blocks, err := t.inner.Run(ctx, input)
	if err != nil {
		return nil, err
	}

	// Validate each text block's content against the output schema.
	for _, b := range blocks {
		tb, ok := b.(*helix.TextBlock)
		if !ok {
			continue
		}

		var parsed any
		if err := json.Unmarshal([]byte(tb.Text), &parsed); err != nil {
			return nil, fmt.Errorf("structured output: invalid JSON: %w", err)
		}

		if err := t.outputSchema.Validate(parsed); err != nil {
			return nil, fmt.Errorf("structured output validation failed: %w", err)
		}
	}

	return blocks, nil
}

// EnforceStructuredOutput wraps a Tool so that its output is validated
// against the JSON Schema derived from Out. If the tool's output doesn't
// conform to the schema, Run returns an error.
//
// Example:
//
//	wrapped := schema.EnforceStructuredOutput[MyOutput](baseTool)
func EnforceStructuredOutput[Out any](tool helix.Tool) helix.Tool {
	return &structuredTool{
		inner:        tool,
		outputSchema: For[Out](),
	}
}

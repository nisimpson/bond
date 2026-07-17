package bondtest

import (
	"context"
	"encoding/json"

	"github.com/nisimpson/bond"
)

// textFromBlock extracts text from the first TextBlock in a message.
func TextFromBlock(msg bond.Message) string {
	for _, b := range msg.Content {
		if tb, ok := b.(*bond.TextBlock); ok {
			return tb.Text
		}
	}
	return ""
}

type FakeTool struct {
	ToolName string
	ToolDesc string
	RunFn    func(context.Context, json.RawMessage) ([]bond.Block, error)
}

func (t *FakeTool) Name() string                { return t.ToolName }
func (t *FakeTool) Description() string         { return t.ToolDesc }
func (t *FakeTool) InputSchema() json.Marshaler { return json.RawMessage(`{"type":"object"}`) }
func (t *FakeTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	if t.RunFn != nil {
		return t.RunFn(ctx, input)
	}
	return []bond.Block{&bond.TextBlock{Text: "ok"}}, nil
}

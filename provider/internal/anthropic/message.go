// Requirement: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6 — map Bond messages to Anthropic format
// Requirement: 3.1, 3.2, 3.3 — map Bond tools to Anthropic tool definitions
package anthropic

import (
	"encoding/json"

	bond "github.com/nisimpson/bond"
)

// MapMessages converts Bond messages to Anthropic message format.
// ToolResultBlocks are inlined as content blocks within the same message
// (Anthropic expects them in user messages, unlike OpenAI's separate tool role).
func MapMessages(messages []bond.Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		role := mapRole(msg.Role)
		blocks := mapBlocks(msg.Content)
		out = append(out, Message{Role: role, Content: blocks})
	}
	return out
}

// MapTools converts Bond tools to Anthropic tool definitions.
// Returns nil, nil if the input slice is nil or empty.
// Returns an error if any tool's input schema cannot be marshaled to JSON.
func MapTools(tools []bond.Tool) ([]Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]Tool, len(tools))
	for i, t := range tools {
		schema := json.RawMessage(`{"type":"object"}`)
		if t.InputSchema() != nil {
			data, err := json.Marshal(t.InputSchema())
			if err != nil {
				return nil, err
			}
			schema = data
		}
		out[i] = Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: schema,
		}
	}
	return out, nil
}

// mapRole converts a Bond role to the corresponding Anthropic role string.
func mapRole(r bond.Role) string {
	switch r {
	case bond.RoleAssistant:
		return "assistant"
	default:
		return "user"
	}
}

// mapBlocks converts Bond content blocks to Anthropic ContentBlocks.
func mapBlocks(blocks []bond.Block) []ContentBlock {
	out := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch block := b.(type) {
		case *bond.TextBlock:
			out = append(out, ContentBlock{Type: "text", Text: block.Text})
		case *bond.ToolUseBlock:
			out = append(out, ContentBlock{
				Type:  "tool_use",
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		case *bond.ToolResultBlock:
			content := mapBlocks(block.Content)
			out = append(out, ContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ToolUseID,
				Content:   content,
				IsError:   block.IsError,
			})
		}
	}
	return out
}

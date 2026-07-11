// Requirement: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6 — map Bond messages to OpenAI format
// Requirement: 3.1, 3.2, 3.3 — map Bond tools to OpenAI tool definitions
package openai

import (
	"encoding/json"
	"fmt"

	bond "github.com/nisimpson/bond"
)

// MapMessages converts Bond messages to OpenAI message format.
// If systemPrompt is non-empty, it is prepended as a system message.
func MapMessages(messages []bond.Message, systemPrompt string) []Message {
	var out []Message

	// Prepend system prompt if provided.
	if systemPrompt != "" {
		out = append(out, Message{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	for _, msg := range messages {
		// Determine OpenAI role from Bond role.
		role := mapRole(msg.Role)

		var content string
		var toolCalls []ToolCall

		for _, block := range msg.Content {
			switch b := block.(type) {
			case *bond.TextBlock:
				content += b.Text

			case *bond.ToolUseBlock:
				toolCalls = append(toolCalls, ToolCall{
					ID:   b.ID,
					Type: "function",
					Function: FunctionCall{
						Name:      b.Name,
						Arguments: string(b.Input),
					},
				})

			case *bond.ToolResultBlock:
				// ToolResultBlock emits a separate "tool" role message.
				resultContent := extractToolResultText(b)
				out = append(out, Message{
					Role:       "tool",
					ToolCallID: b.ToolUseID,
					Content:    resultContent,
				})
			}
		}

		// Emit the main message if it has text content or tool calls.
		if content != "" || len(toolCalls) > 0 {
			m := Message{
				Role:      role,
				Content:   content,
				ToolCalls: toolCalls,
			}
			out = append(out, m)
		}
	}

	return out
}

// MapTools converts Bond tools to OpenAI tool definitions.
// Returns nil, nil if the input slice is nil or empty.
// Returns an error if any tool's input schema cannot be marshaled to JSON.
func MapTools(tools []bond.Tool) ([]Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	out := make([]Tool, len(tools))
	for i, t := range tools {
		params, err := json.Marshal(t.InputSchema())
		if err != nil {
			return nil, fmt.Errorf("marshal schema for tool %q: %w", t.Name(), err)
		}
		out[i] = Tool{
			Type: "function",
			Function: FunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		}
	}
	return out, nil
}

// mapRole converts a Bond role to the corresponding OpenAI role string.
func mapRole(r bond.Role) string {
	switch r {
	case bond.RoleUser:
		return "user"
	case bond.RoleAssistant:
		return "assistant"
	default:
		return string(r)
	}
}

// extractToolResultText concatenates text from all TextBlocks in a ToolResultBlock's content.
func extractToolResultText(b *bond.ToolResultBlock) string {
	var text string
	for _, block := range b.Content {
		if tb, ok := block.(*bond.TextBlock); ok {
			text += tb.Text
		}
	}
	return text
}

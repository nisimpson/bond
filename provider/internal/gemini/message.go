// Requirement: 7.1, 7.2, 7.3, 7.4 — map Bond messages and tools to Gemini format
package gemini

import (
	"encoding/json"
	"fmt"

	bond "github.com/nisimpson/bond"
)

// MapMessages converts Bond messages to Gemini content format.
// RoleUser maps to "user" and RoleAssistant maps to "model".
func MapMessages(messages []bond.Message) []Content {
	out := make([]Content, 0, len(messages))
	for _, msg := range messages {
		role := mapRole(msg.Role)
		parts := mapParts(msg.Content)
		out = append(out, Content{Role: role, Parts: parts})
	}
	return out
}

// MapTools converts Bond tools to Gemini functionDeclarations wrapped in a
// single [ToolConfig]. Returns nil, nil if the input slice is nil or empty.
func MapTools(tools []bond.Tool) ([]ToolConfig, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	decls := make([]FunctionDeclaration, len(tools))
	for i, t := range tools {
		var params json.RawMessage
		if t.InputSchema() != nil {
			data, err := json.Marshal(t.InputSchema())
			if err != nil {
				return nil, fmt.Errorf("marshal schema for tool %q: %w", t.Name(), err)
			}
			params = data
		}
		decls[i] = FunctionDeclaration{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  params,
		}
	}
	return []ToolConfig{{FunctionDeclarations: decls}}, nil
}

// mapRole converts a Bond role to the corresponding Gemini role string.
func mapRole(r bond.Role) string {
	switch r {
	case bond.RoleAssistant:
		return "model"
	default:
		return "user"
	}
}

// mapParts converts Bond content blocks to Gemini parts.
func mapParts(blocks []bond.Block) []Part {
	out := make([]Part, 0, len(blocks))
	for _, b := range blocks {
		switch block := b.(type) {
		case *bond.TextBlock:
			out = append(out, Part{Text: block.Text})
		case *bond.ToolUseBlock:
			out = append(out, Part{
				FunctionCall: &FunctionCall{
					Name: block.Name,
					Args: block.Input,
				},
			})
		case *bond.ToolResultBlock:
			name := block.Name
			if name == "" {
				name = block.ToolUseID
			}
			response := extractResponseJSON(block)
			out = append(out, Part{
				FunctionResponse: &FunctionResponse{
					Name:     name,
					Response: response,
				},
			})
		}
	}
	return out
}

// extractResponseJSON concatenates text from all TextBlocks in a ToolResultBlock's
// content and returns it as JSON. If the concatenated text is already valid JSON,
// it is returned as-is; otherwise it is wrapped as {"result": "...text..."}.
func extractResponseJSON(block *bond.ToolResultBlock) json.RawMessage {
	var text string
	for _, b := range block.Content {
		if tb, ok := b.(*bond.TextBlock); ok {
			text += tb.Text
		}
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	data, _ := json.Marshal(map[string]string{"result": text})
	return data
}

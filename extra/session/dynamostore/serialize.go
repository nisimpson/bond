package dynamostore

import (
	"encoding/json"

	"github.com/nisimpson/bond"
)

// Requirement: CONV-8.5 — Message serialization helpers for DynamoDB storage.
// Handles TextBlock, ToolUseBlock, ToolResultBlock serialization.
// Omits MediaBlock with io.Reader sources; persists only SourceURI-based media.

// serializedMessage is the JSON representation of a bond.Message for DynamoDB storage.
type serializedMessage struct {
	Role    string            `json:"role"`
	Content []serializedBlock `json:"content"`
}

// serializedBlock is the JSON representation of a bond.Block for DynamoDB storage.
type serializedBlock struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// textBlockData is the JSON payload for a TextBlock.
type textBlockData struct {
	Text string `json:"text"`
}

// toolUseBlockData is the JSON payload for a ToolUseBlock.
type toolUseBlockData struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// toolResultBlockData is the JSON payload for a ToolResultBlock.
type toolResultBlockData struct {
	ToolUseID string            `json:"tool_use_id"`
	Content   []serializedBlock `json:"content"`
	IsError   bool              `json:"is_error"`
}

// mediaBlockData is the JSON payload for a MediaBlock (SourceURI only).
type mediaBlockData struct {
	Type      string `json:"media_type"`
	MIMEType  string `json:"mime_type"`
	SourceURI string `json:"source_uri"`
}

// serializeMessages converts a message slice to JSON bytes for DynamoDB storage.
func serializeMessages(messages []bond.Message) ([]byte, error) {
	serialized := make([]serializedMessage, len(messages))
	for i, msg := range messages {
		sm := serializedMessage{
			Role:    string(msg.Role),
			Content: make([]serializedBlock, 0, len(msg.Content)),
		}
		for _, block := range msg.Content {
			sb, err := serializeBlock(block)
			if err != nil {
				return nil, err
			}
			if sb != nil {
				sm.Content = append(sm.Content, *sb)
			}
		}
		serialized[i] = sm
	}
	return json.Marshal(serialized)
}

// serializeBlock converts a single bond.Block to its serialized form.
// Returns nil for blocks that cannot be serialized (e.g., MediaBlock with io.Reader).
func serializeBlock(block bond.Block) (*serializedBlock, error) {
	switch b := block.(type) {
	case *bond.TextBlock:
		data, err := json.Marshal(textBlockData{Text: b.Text})
		if err != nil {
			return nil, err
		}
		return &serializedBlock{Type: "text", Data: data}, nil

	case *bond.ToolUseBlock:
		data, err := json.Marshal(toolUseBlockData{
			ID:    b.ID,
			Name:  b.Name,
			Input: b.Input,
		})
		if err != nil {
			return nil, err
		}
		return &serializedBlock{Type: "tool_use", Data: data}, nil

	case *bond.ToolResultBlock:
		contentBlocks := make([]serializedBlock, 0, len(b.Content))
		for _, inner := range b.Content {
			sb, err := serializeBlock(inner)
			if err != nil {
				return nil, err
			}
			if sb != nil {
				contentBlocks = append(contentBlocks, *sb)
			}
		}
		data, err := json.Marshal(toolResultBlockData{
			ToolUseID: b.ToolUseID,
			Content:   contentBlocks,
			IsError:   b.IsError,
		})
		if err != nil {
			return nil, err
		}
		return &serializedBlock{Type: "tool_result", Data: data}, nil

	case *bond.MediaBlock:
		// Only persist MediaBlocks that have a SourceURI (no io.Reader).
		if b.SourceURI == "" {
			return nil, nil
		}
		data, err := json.Marshal(mediaBlockData{
			Type:      string(b.Type),
			MIMEType:  b.MIMEType,
			SourceURI: b.SourceURI,
		})
		if err != nil {
			return nil, err
		}
		return &serializedBlock{Type: "media", Data: data}, nil

	default:
		// Unknown block types are skipped.
		return nil, nil
	}
}

// deserializeMessages converts JSON bytes back to a message slice.
func deserializeMessages(data []byte) ([]bond.Message, error) {
	var serialized []serializedMessage
	if err := json.Unmarshal(data, &serialized); err != nil {
		return nil, err
	}

	messages := make([]bond.Message, len(serialized))
	for i, sm := range serialized {
		msg := bond.Message{
			Role:    bond.Role(sm.Role),
			Content: make([]bond.Block, 0, len(sm.Content)),
		}
		for _, sb := range sm.Content {
			block, err := deserializeBlock(sb)
			if err != nil {
				return nil, err
			}
			if block != nil {
				msg.Content = append(msg.Content, block)
			}
		}
		messages[i] = msg
	}
	return messages, nil
}

// deserializeBlock converts a serialized block back to a bond.Block.
func deserializeBlock(sb serializedBlock) (bond.Block, error) {
	switch sb.Type {
	case "text":
		var d textBlockData
		if err := json.Unmarshal(sb.Data, &d); err != nil {
			return nil, err
		}
		return &bond.TextBlock{Text: d.Text}, nil

	case "tool_use":
		var d toolUseBlockData
		if err := json.Unmarshal(sb.Data, &d); err != nil {
			return nil, err
		}
		return &bond.ToolUseBlock{ID: d.ID, Name: d.Name, Input: d.Input}, nil

	case "tool_result":
		var d toolResultBlockData
		if err := json.Unmarshal(sb.Data, &d); err != nil {
			return nil, err
		}
		content := make([]bond.Block, 0, len(d.Content))
		for _, inner := range d.Content {
			block, err := deserializeBlock(inner)
			if err != nil {
				return nil, err
			}
			if block != nil {
				content = append(content, block)
			}
		}
		return &bond.ToolResultBlock{
			ToolUseID: d.ToolUseID,
			Content:   content,
			IsError:   d.IsError,
		}, nil

	case "media":
		var d mediaBlockData
		if err := json.Unmarshal(sb.Data, &d); err != nil {
			return nil, err
		}
		return &bond.MediaBlock{
			Type:      bond.MediaType(d.Type),
			MIMEType:  d.MIMEType,
			SourceURI: d.SourceURI,
		}, nil

	default:
		// Unknown block types are skipped during deserialization.
		return nil, nil
	}
}

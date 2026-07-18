// Package anthropic provides types and helpers for the Anthropic Messages streaming API.
package anthropic

import "encoding/json"

// MessagesRequest is the POST body for /v1/messages.
type MessagesRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	System      string    `json:"system,omitempty"`
	MaxTokens   int       `json:"max_tokens"`
	Stream      bool      `json:"stream"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
}

// Message is the Anthropic message format with role and content blocks.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a polymorphic content entry supporting text, tool_use,
// and tool_result block types via the Type field and optional fields.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   []ContentBlock  `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Tool is an Anthropic tool definition with name, description, and input schema.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ContentBlockStart is the SSE data payload for content_block_start events.
type ContentBlockStart struct {
	Index        int          `json:"index"`
	ContentBlock ContentBlock `json:"content_block"`
}

// ContentBlockDelta is the SSE data payload for content_block_delta events.
type ContentBlockDelta struct {
	Index int   `json:"index"`
	Delta Delta `json:"delta"`
}

// Delta holds a delta fragment in a content_block_delta event,
// carrying either a text chunk or partial JSON for tool input.
type Delta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// MessageDelta is the SSE data payload for message_delta events,
// carrying the final stop reason for the message.
type MessageDelta struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
}

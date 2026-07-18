package bond

import (
	"context"
	"encoding/json"
	"io"
)

// Role represents the sender of a message in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message represents a single turn in a conversation.
type Message struct {
	Role    Role
	Content []Block
	// Metadata holds provider-specific annotations (reasoning traces,
	// citations, guard content, cache points, etc.) that don't map
	// universally across providers.
	Metadata map[string]any
}

// Block is the interface satisfied by all content blocks.
type Block interface {
	blockType() BlockType
}

// TextBlock holds plain text content.
type TextBlock struct {
	Text string
}

// MediaType describes the kind of binary content in a MediaBlock.
type MediaType string

const (
	MediaTypeImage    MediaType = "image"
	MediaTypeAudio    MediaType = "audio"
	MediaTypeVideo    MediaType = "video"
	MediaTypeDocument MediaType = "document"
)

// MediaBlock holds binary content (images, audio, video, documents).
type MediaBlock struct {
	Type      MediaType
	MIMEType  string
	Source    io.Reader // content payload; wrap bytes with bytes.NewReader
	SourceURI string    // optional URI reference (e.g. S3 key, URL)
}

// ToolUseBlock represents the model requesting a tool invocation.
type ToolUseBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResultBlock holds the response from a tool invocation.
type ToolResultBlock struct {
	ToolUseID string
	Name      string  // Name of the tool that produced this result.
	Content   []Block // typically TextBlock or MediaBlock
	IsError   bool
}

type BlockType string

const (
	BlockTypeText       BlockType = "text"
	BlockTypeMedia      BlockType = "media"
	BlockTypeToolUse    BlockType = "tool_use"
	BlockTypeToolResult BlockType = "tool_result"
)

func (*TextBlock) blockType() BlockType       { return BlockTypeText }
func (*MediaBlock) blockType() BlockType      { return BlockTypeMedia }
func (*ToolResultBlock) blockType() BlockType { return BlockTypeToolResult }
func (*ToolUseBlock) blockType() BlockType    { return BlockTypeToolUse }

// TextPrompt is a convenience helper that wraps a plain string into
// the []Message format expected by Agent.Stream.
func TextPrompt(text string) []Message {
	return []Message{
		{
			Role:    RoleUser,
			Content: []Block{&TextBlock{Text: text}},
		},
	}
}

// ImagePrompt creates a user message with text and an image from a URI.
func ImagePrompt(text, imageURI, mimeType string) []Message {
	return []Message{
		{
			Role: RoleUser,
			Content: []Block{
				&TextBlock{Text: text},
				&MediaBlock{Type: MediaTypeImage, MIMEType: mimeType, SourceURI: imageURI},
			},
		},
	}
}

// MultiBlockPrompt creates a user message from arbitrary blocks.
func MultiBlockPrompt(blocks ...Block) []Message {
	return []Message{
		{
			Role:    RoleUser,
			Content: blocks,
		},
	}
}

// Conversation builds a message list from alternating user/assistant strings.
// The first string is user, second is assistant, third is user, etc.
// Useful for constructing few-shot examples or test fixtures.
func Conversation(turns ...string) []Message {
	messages := make([]Message, len(turns))
	for i, text := range turns {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		messages[i] = Message{
			Role:    role,
			Content: []Block{&TextBlock{Text: text}},
		}
	}
	return messages
}

// toolsContextKey is the context key for tools available to the agent.
type toolsContextKey struct{}

// withTools attaches tools to a context for providers to access.
func withTools(ctx context.Context, tools []Tool) context.Context {
	return context.WithValue(ctx, toolsContextKey{}, tools)
}

// ToolsFromContext retrieves the tools available for the current invocation.
// Providers use this to include tool definitions in API requests. Returns nil
// if no tools are configured.
func ToolsFromContext(ctx context.Context) []Tool {
	tools, _ := ctx.Value(toolsContextKey{}).([]Tool)
	return tools
}

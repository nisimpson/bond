package helix

import (
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

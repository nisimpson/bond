package session

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/nisimpson/bond"
)

// Requirement: CONV-7.1, CONV-7.2, CONV-7.3, CONV-7.4, CONV-7.5 — in-memory session store

// InMemoryStore is a thread-safe, in-process SessionStore for development and testing.
// It stores sessions in a Go map protected by a sync.RWMutex.
// All Save and Load operations use deep copies to prevent aliasing with caller data.
type InMemoryStore struct {
	mu       sync.RWMutex
	sessions map[string][]bond.Message
}

// NewInMemoryStore creates a new InMemoryStore ready for use.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		sessions: make(map[string][]bond.Message),
	}
}

// Load retrieves the stored messages for the given session.
// Returns an empty slice (not nil) and nil error if no data exists for the session ID.
func (s *InMemoryStore) Load(_ context.Context, sessionID string) ([]bond.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stored, ok := s.sessions[sessionID]
	if !ok {
		return []bond.Message{}, nil
	}

	return deepCopyMessages(stored), nil
}

// Save persists the message slice for the given session, overwriting any previous data.
// A deep copy of the messages is stored to prevent aliasing with the caller's data.
func (s *InMemoryStore) Save(_ context.Context, sessionID string, messages []bond.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sessionID] = deepCopyMessages(messages)
	return nil
}

// deepCopyMessages creates a deep copy of a message slice.
func deepCopyMessages(messages []bond.Message) []bond.Message {
	if messages == nil {
		return []bond.Message{}
	}

	copied := make([]bond.Message, len(messages))
	for i, msg := range messages {
		copied[i] = deepCopyMessage(msg)
	}
	return copied
}

// deepCopyMessage creates a deep copy of a single message.
func deepCopyMessage(msg bond.Message) bond.Message {
	result := bond.Message{
		Role: msg.Role,
	}

	// Deep copy content blocks.
	if msg.Content != nil {
		result.Content = make([]bond.Block, len(msg.Content))
		for i, block := range msg.Content {
			result.Content[i] = deepCopyBlock(block)
		}
	}

	// Deep copy metadata map.
	if msg.Metadata != nil {
		result.Metadata = deepCopyMetadata(msg.Metadata)
	}

	return result
}

// deepCopyBlock creates a deep copy of a content block.
func deepCopyBlock(block bond.Block) bond.Block {
	switch b := block.(type) {
	case *bond.TextBlock:
		return &bond.TextBlock{Text: b.Text}
	case *bond.MediaBlock:
		// For MediaBlock, copy SourceURI but not the io.Reader (can't deep-copy readers).
		return &bond.MediaBlock{
			Type:      b.Type,
			MIMEType:  b.MIMEType,
			SourceURI: b.SourceURI,
		}
	case *bond.ToolUseBlock:
		var inputCopy json.RawMessage
		if b.Input != nil {
			inputCopy = make(json.RawMessage, len(b.Input))
			copy(inputCopy, b.Input)
		}
		return &bond.ToolUseBlock{
			ID:    b.ID,
			Name:  b.Name,
			Input: inputCopy,
		}
	case *bond.ToolResultBlock:
		result := &bond.ToolResultBlock{
			ToolUseID: b.ToolUseID,
			IsError:   b.IsError,
		}
		if b.Content != nil {
			result.Content = make([]bond.Block, len(b.Content))
			for i, inner := range b.Content {
				result.Content[i] = deepCopyBlock(inner)
			}
		}
		return result
	default:
		// Unknown block type: return as-is (best effort).
		return block
	}
}

// deepCopyMetadata creates a deep copy of a metadata map.
// Values are copied using JSON round-trip for nested structures.
func deepCopyMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = deepCopyValue(v)
	}
	return result
}

// deepCopyValue copies a metadata value. For maps and slices, uses JSON round-trip
// to ensure deep copies. Scalar values are copied directly.
func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, inner := range val {
			cp[k] = deepCopyValue(inner)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, inner := range val {
			cp[i] = deepCopyValue(inner)
		}
		return cp
	default:
		// Scalars (string, int, float64, bool, nil) are value types or immutable.
		return v
	}
}

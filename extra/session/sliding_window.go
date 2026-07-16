package session

import (
	"context"
	"fmt"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
)

// Requirement: CONV-2.1, CONV-2.5 — sliding window construction and validation

// SlidingWindowOptions configures a [SlidingWindowManager].
type SlidingWindowOptions struct {
	// WindowSize is the maximum number of user/assistant message pairs to retain.
	// Must be positive.
	WindowSize int
}

// SlidingWindowManager keeps the last N user/assistant message pairs,
// always preserving the system preamble (messages before the first user message).
type SlidingWindowManager struct {
	windowSize int
}

// compile-time interface compliance check
var _ agent.HistoryPolicy = (*SlidingWindowManager)(nil)

// NewSlidingWindowManager creates a [SlidingWindowManager].
// Returns an error if opts.WindowSize <= 0.
func NewSlidingWindowManager(opts SlidingWindowOptions) (*SlidingWindowManager, error) {
	if opts.WindowSize <= 0 {
		return nil, fmt.Errorf("session: window size must be positive, got %d", opts.WindowSize)
	}
	return &SlidingWindowManager{windowSize: opts.WindowSize}, nil
}

// Requirement: CONV-2.2, CONV-2.3, CONV-2.4 — sliding window select logic

// Select returns a subset of messages that retains at most the last N user/assistant
// pairs, prepended with the system preamble. A "pair" is defined as a user message
// followed by zero or more assistant messages until the next user message. If the
// total number of pairs is within the window size, the original slice is returned
// unchanged.
func (m *SlidingWindowManager) Select(_ context.Context, messages []bond.Message) ([]bond.Message, error) {
	// Identify the preamble: all messages before the first RoleUser message.
	preambleEnd := 0
	for i, msg := range messages {
		if msg.Role == bond.RoleUser {
			preambleEnd = i
			break
		}
		// If we reach the end without finding a user message, everything is preamble.
		if i == len(messages)-1 {
			return messages, nil
		}
	}

	conversation := messages[preambleEnd:]

	// Find indices of all user messages in the conversation portion.
	var userIndices []int
	for i, msg := range conversation {
		if msg.Role == bond.RoleUser {
			userIndices = append(userIndices, i)
		}
	}

	// If the number of pairs is within the window, return unchanged.
	if len(userIndices) <= m.windowSize {
		return messages, nil
	}

	// Keep the last N pairs: find the Nth-to-last user message and include
	// everything from that point onwards.
	cutIndex := userIndices[len(userIndices)-m.windowSize]

	// Build result: preamble + last N pairs
	result := make([]bond.Message, 0, preambleEnd+len(conversation)-cutIndex)
	result = append(result, messages[:preambleEnd]...)
	result = append(result, conversation[cutIndex:]...)

	return result, nil
}

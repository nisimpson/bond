package conversation

import (
	"context"

	"github.com/nisimpson/bond"
)

// Requirement: CONV-1.1, CONV-1.2, CONV-1.3, CONV-1.4 — conversation manager interface

// ConversationManager trims a message slice to fit within context constraints.
type ConversationManager interface {
	// Trim returns a subset of messages that satisfies the manager's constraints.
	// The returned slice preserves the relative ordering of the input.
	// Returns an error if trimming cannot produce a valid result.
	Trim(ctx context.Context, messages []bond.Message) ([]bond.Message, error)
}

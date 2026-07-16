package agent

import (
	"context"

	"github.com/nisimpson/bond"
)

// HistoryPolicy selects a subset of messages from a conversation.
// Implementations are used for conversation trimming (extra/session),
// swarm transfer context carry-over, and summarization workflows.
//
// The returned slice must preserve the relative ordering of the input.
type HistoryPolicy interface {
	// Select returns a subset of messages satisfying the policy's constraints.
	// The returned slice preserves the relative ordering of the input.
	// Returns an error if the policy cannot produce a valid result.
	Select(ctx context.Context, messages []bond.Message) ([]bond.Message, error)
}

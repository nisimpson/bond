package session

import (
	"context"

	"github.com/nisimpson/bond"
)

// Summarizer produces a summary message from a slice of messages using an LLM.
// Implementations typically delegate to an agent or model call to condense
// conversation history into a single representative message.
type Summarizer interface {
	// Summarize condenses the given messages into a single summary message.
	Summarize(ctx context.Context, messages []bond.Message) (bond.Message, error)
}

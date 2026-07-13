package session

import (
	"context"

	"github.com/nisimpson/bond"
)

// Requirement: CONV-6.1, CONV-6.2, CONV-6.3, CONV-6.4, CONV-6.5 — session store interface

// Store persists and retrieves conversation history keyed by session ID.
type Store interface {
	// Load retrieves the stored messages for the given session.
	// Returns an empty slice (not nil) and nil error if no data exists.
	Load(ctx context.Context, sessionID string) ([]bond.Message, error)
	// Save persists the message slice for the given session, overwriting any previous data.
	Save(ctx context.Context, sessionID string, messages []bond.Message) error
}

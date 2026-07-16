package approval

import (
	"context"
	"fmt"

	"github.com/nisimpson/bond"
)

// ErrInterrupted signals that execution was suspended pending external approval.
// It wraps [bond.ErrAbort] so that the agent loop stops gracefully, but callers
// can distinguish it from a hard denial via errors.Is(err, ErrInterrupted).
var ErrInterrupted = fmt.Errorf("%w: approval: interrupted pending external input", bond.ErrAbort)

// Store persists and retrieves approval records keyed by ID.
type Store interface {
	// Load retrieves a record by ID. Returns (nil, nil) if not found.
	Load(ctx context.Context, id string) (*Record, error)
	// Save persists a record, overwriting any existing record with the same ID.
	Save(ctx context.Context, record *Record) error
	// Delete removes a record by ID. No-op if not found.
	Delete(ctx context.Context, id string) error
}

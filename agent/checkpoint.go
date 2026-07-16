package agent

import (
	"context"
	"errors"

	"github.com/nisimpson/bond"
)

// ErrSnapshotNotFound is returned by [CheckpointStore].Load when the
// requested snapshot does not exist.
var ErrSnapshotNotFound = errors.New("agent: snapshot not found")

// CheckpointStore persists and retrieves graph execution snapshots.
// Implementations must return [ErrSnapshotNotFound] from Load when the
// requested snapshot ID does not exist.
type CheckpointStore interface {
	// Save persists a snapshot. Overwrites any existing snapshot with
	// the same ID.
	Save(ctx context.Context, id string, snapshot *Snapshot) error
	// Load retrieves a snapshot by ID. Implementations must return
	// [ErrSnapshotNotFound] if the snapshot does not exist.
	Load(ctx context.Context, id string) (*Snapshot, error)
	// Delete removes a snapshot by ID. No-op if not found.
	Delete(ctx context.Context, id string) error
}

// Snapshot captures graph execution state at a point in time.
// It is serializable to JSON for storage in any backend.
type Snapshot struct {
	ID       string         `json:"id"`
	Node     string         `json:"node"`      // node that just completed
	NextNode string         `json:"next_node"` // node to execute next
	State    map[string]any `json:"state"`     // serialized shared state
	History  []bond.Message `json:"history"`   // conversation history
}

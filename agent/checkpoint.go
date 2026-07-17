package agent

import (
	"context"
	"errors"
	"sync"

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

// Compile-time check that InMemoryCheckpointStore implements [CheckpointStore].
var _ CheckpointStore = (*InMemoryCheckpointStore)(nil)

// InMemoryCheckpointStore is a thread-safe, in-process [CheckpointStore]
// for development and testing. It stores snapshots in a Go map
// protected by a [sync.RWMutex].
type InMemoryCheckpointStore struct {
	// mu guards snapshots.
	mu        sync.RWMutex
	snapshots map[string]*Snapshot
}

// NewInMemoryCheckpointStore creates a new [InMemoryCheckpointStore] ready for use.
func NewInMemoryCheckpointStore() *InMemoryCheckpointStore {
	return &InMemoryCheckpointStore{
		snapshots: make(map[string]*Snapshot),
	}
}

// Save persists a snapshot, overwriting any existing snapshot with the same ID.
func (s *InMemoryCheckpointStore) Save(_ context.Context, id string, snapshot *Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[id] = snapshot
	return nil
}

// Load retrieves a snapshot by ID. Returns [ErrSnapshotNotFound]
// if the snapshot does not exist.
//
// The returned pointer references the stored object directly. Callers
// that need mutation isolation should copy the value before modifying it.
func (s *InMemoryCheckpointStore) Load(_ context.Context, id string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, ErrSnapshotNotFound
	}
	return snap, nil
}

// Delete removes a snapshot by ID. It is a no-op if the snapshot does not exist.
func (s *InMemoryCheckpointStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, id)
	return nil
}

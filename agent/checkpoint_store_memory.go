package agent

import (
	"context"
	"sync"
)

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

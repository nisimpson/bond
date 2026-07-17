package approval

import (
	"context"
	"fmt"
	"sync"

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

// Compile-time check that InMemoryStore implements [Store].
var _ Store = (*InMemoryStore)(nil)

// InMemoryStore is a thread-safe, in-process [Store] for development
// and testing. It stores records in a map protected by a read-write mutex.
type InMemoryStore struct {
	// mu protects records from concurrent access.
	mu      sync.RWMutex
	records map[string]*Record
}

// NewInMemoryStore creates an empty [InMemoryStore] ready for use.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		records: make(map[string]*Record),
	}
}

// Load retrieves a record by ID. Returns (nil, nil) if not found.
//
// The returned pointer references the stored object directly. Callers
// that need mutation isolation should copy the value before modifying it.
func (s *InMemoryStore) Load(_ context.Context, id string) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

// Save persists a record, overwriting any existing record with the same ID.
func (s *InMemoryStore) Save(_ context.Context, record *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
	return nil
}

// Delete removes a record by ID. No-op if not found.
func (s *InMemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}

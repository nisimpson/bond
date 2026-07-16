package approval_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/approval"
)

// testBeforeEvent is a test hook event type satisfying bond.BeforeHookEvent.
type testBeforeEvent struct {
	Key string
}

func (*testBeforeEvent) HookEvent()       {}
func (*testBeforeEvent) BeforeHookEvent() {}

// randomKey generates a non-empty ASCII key of length 1..maxLen.
func randomKey(r *rand.Rand, maxLen int) string {
	n := r.Intn(maxLen) + 1
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(26))
	}
	return string(b)
}

// Feature: advanced-orchestration, Property 5: AsyncGate idempotency with KeyFunc
// **Validates: Requirements 1.3, 1.4, 1.5, 1.9, 1.10**

// TestProperty_AsyncGatePendingPath verifies that on first encounter with no
// existing record, the gate creates a pending record and returns ErrInterrupted.
// On subsequent calls with the same key (while pending), the gate still returns
// ErrInterrupted (idempotent).
func TestProperty_AsyncGatePendingPath(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := approval.NewInMemoryStore()

		key := randomKey(r, 20)
		gate := approval.NewAsyncGate(approval.AsyncGateOptions[*testBeforeEvent]{
			Store:   store,
			KeyFunc: func(e *testBeforeEvent) string { return e.Key },
		})

		registry := &bond.HookRegistry{}
		gate.Register(registry)

		event := &testBeforeEvent{Key: key}

		// First call: no existing record → pending → ErrInterrupted.
		err := registry.Notify(ctx, event)
		if !errors.Is(err, approval.ErrInterrupted) {
			t.Logf("first call: expected ErrInterrupted, got: %v", err)
			return false
		}

		// Verify record exists with pending status.
		record, loadErr := store.Load(ctx, key)
		if loadErr != nil {
			t.Logf("load after first call failed: %v", loadErr)
			return false
		}
		if record == nil {
			t.Logf("expected record to exist after first call")
			return false
		}
		if record.Status != approval.StatusPending {
			t.Logf("expected pending status, got %q", record.Status)
			return false
		}

		// Second call with same key while pending → ErrInterrupted (idempotent).
		repeatCount := 1 + r.Intn(5)
		for i := range repeatCount {
			err = registry.Notify(ctx, event)
			if !errors.Is(err, approval.ErrInterrupted) {
				t.Logf("call %d: expected ErrInterrupted, got: %v", i+2, err)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: AsyncGate pending path — failed: %v", err)
	}
}

// TestProperty_AsyncGateApprovedPath verifies that after Approve is called,
// the gate returns nil (execution continues).
func TestProperty_AsyncGateApprovedPath(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := approval.NewInMemoryStore()

		key := randomKey(r, 20)
		gate := approval.NewAsyncGate(approval.AsyncGateOptions[*testBeforeEvent]{
			Store:   store,
			KeyFunc: func(e *testBeforeEvent) string { return e.Key },
		})

		registry := &bond.HookRegistry{}
		gate.Register(registry)

		event := &testBeforeEvent{Key: key}

		// First call creates pending record.
		err := registry.Notify(ctx, event)
		if !errors.Is(err, approval.ErrInterrupted) {
			t.Logf("first call: expected ErrInterrupted, got: %v", err)
			return false
		}

		// Approve the record.
		if err := approval.ApprovePending(ctx, store, key); err != nil {
			t.Logf("ApprovePending failed: %v", err)
			return false
		}

		// Subsequent call: approved → nil (continue).
		err = registry.Notify(ctx, event)
		if err != nil {
			t.Logf("after approve: expected nil error, got: %v", err)
			return false
		}

		// Repeated calls after approval should still return nil.
		repeatCount := 1 + r.Intn(5)
		for i := range repeatCount {
			err = registry.Notify(ctx, event)
			if err != nil {
				t.Logf("approved call %d: expected nil, got: %v", i+2, err)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: AsyncGate approved path — failed: %v", err)
	}
}

// TestProperty_AsyncGateDeniedPath verifies that after Deny is called,
// the gate returns an error wrapping bond.ErrAbort.
func TestProperty_AsyncGateDeniedPath(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := approval.NewInMemoryStore()

		key := randomKey(r, 20)
		reason := fmt.Sprintf("denied-%s", randomKey(r, 10))
		gate := approval.NewAsyncGate(approval.AsyncGateOptions[*testBeforeEvent]{
			Store:   store,
			KeyFunc: func(e *testBeforeEvent) string { return e.Key },
		})

		registry := &bond.HookRegistry{}
		gate.Register(registry)

		event := &testBeforeEvent{Key: key}

		// First call creates pending record.
		err := registry.Notify(ctx, event)
		if !errors.Is(err, approval.ErrInterrupted) {
			t.Logf("first call: expected ErrInterrupted, got: %v", err)
			return false
		}

		// Deny the record.
		if err := approval.DenyPending(ctx, store, key, reason); err != nil {
			t.Logf("DenyPending failed: %v", err)
			return false
		}

		// Subsequent call: denied → error wrapping ErrAbort.
		err = registry.Notify(ctx, event)
		if err == nil {
			t.Logf("after deny: expected error, got nil")
			return false
		}
		if !errors.Is(err, bond.ErrAbort) {
			t.Logf("after deny: expected ErrAbort in chain, got: %v", err)
			return false
		}

		// Repeated calls after denial should still return ErrAbort.
		repeatCount := 1 + r.Intn(5)
		for i := range repeatCount {
			err = registry.Notify(ctx, event)
			if err == nil || !errors.Is(err, bond.ErrAbort) {
				t.Logf("denied call %d: expected ErrAbort, got: %v", i+2, err)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: AsyncGate denied path — failed: %v", err)
	}
}

// TestProperty_ErrInterruptedWrapsErrAbort verifies that ErrInterrupted wraps
// bond.ErrAbort so callers can distinguish suspension from other errors.
func TestProperty_ErrInterruptedWrapsErrAbort(t *testing.T) {
	// This is a universal invariant — no randomization needed, but we run
	// it through quick.Check to stay consistent with project conventions.
	f := func(_ int64) bool {
		if !errors.Is(approval.ErrInterrupted, bond.ErrAbort) {
			t.Logf("ErrInterrupted does not wrap ErrAbort")
			return false
		}
		// Also verify they are distinguishable.
		if errors.Is(bond.ErrAbort, approval.ErrInterrupted) {
			// ErrAbort should NOT be ErrInterrupted (it's the parent, not child).
			t.Logf("ErrAbort should not wrap ErrInterrupted (wrong direction)")
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: ErrInterrupted wraps ErrAbort — failed: %v", err)
	}
}

// TestProperty_AsyncGateKeyFuncDeduplication verifies that with a KeyFunc,
// repeated calls with different event instances that produce the same key
// always map to the same record (idempotent deduplication).
func TestProperty_AsyncGateKeyFuncDeduplication(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := approval.NewInMemoryStore()

		key := randomKey(r, 20)
		gate := approval.NewAsyncGate(approval.AsyncGateOptions[*testBeforeEvent]{
			Store:   store,
			KeyFunc: func(e *testBeforeEvent) string { return e.Key },
		})

		registry := &bond.HookRegistry{}
		gate.Register(registry)

		// Create multiple distinct event instances that all map to the same key.
		numEvents := 2 + r.Intn(5)
		events := make([]*testBeforeEvent, numEvents)
		for i := range events {
			events[i] = &testBeforeEvent{Key: key}
		}

		// First event call creates the pending record.
		err := registry.Notify(ctx, events[0])
		if !errors.Is(err, approval.ErrInterrupted) {
			t.Logf("first event: expected ErrInterrupted, got: %v", err)
			return false
		}

		// All subsequent events with same key should hit the same record.
		for i := 1; i < numEvents; i++ {
			err = registry.Notify(ctx, events[i])
			if !errors.Is(err, approval.ErrInterrupted) {
				t.Logf("event %d: expected ErrInterrupted (same pending record), got: %v", i, err)
				return false
			}
		}

		// Verify only one record exists in the store for this key.
		record, loadErr := store.Load(ctx, key)
		if loadErr != nil {
			t.Logf("load failed: %v", loadErr)
			return false
		}
		if record == nil {
			t.Logf("expected exactly one record for key %q", key)
			return false
		}
		if record.ID != key {
			t.Logf("record ID mismatch: got %q, want %q", record.ID, key)
			return false
		}

		// Approve once, then all events with same key should return nil.
		if err := approval.ApprovePending(ctx, store, key); err != nil {
			t.Logf("ApprovePending failed: %v", err)
			return false
		}

		for i := range numEvents {
			err = registry.Notify(ctx, events[i])
			if err != nil {
				t.Logf("after approve, event %d: expected nil, got: %v", i, err)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: AsyncGate KeyFunc deduplication — failed: %v", err)
	}
}

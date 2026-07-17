package agent_test

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
)

// ckptRandomASCII generates a random ASCII string of length 1..maxLen.
func ckptRandomASCII(r *rand.Rand, maxLen int) string {
	n := r.Intn(maxLen) + 1
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(26))
	}
	return string(b)
}

// ckptRandomState generates a random map[string]any with string and int values.
func ckptRandomState(r *rand.Rand) map[string]any {
	n := r.Intn(5)
	if n == 0 {
		return map[string]any{}
	}
	m := make(map[string]any, n)
	for range n {
		key := ckptRandomASCII(r, 10)
		if r.Intn(2) == 0 {
			m[key] = ckptRandomASCII(r, 20)
		} else {
			m[key] = r.Intn(1000)
		}
	}
	return m
}

// ckptRandomHistory generates a random slice of bond.Message values.
func ckptRandomHistory(r *rand.Rand) []bond.Message {
	n := r.Intn(5)
	msgs := make([]bond.Message, n)
	for i := range msgs {
		role := bond.RoleUser
		if r.Intn(2) == 0 {
			role = bond.RoleAssistant
		}
		msgs[i] = bond.Message{
			Role:    role,
			Content: []bond.Block{&bond.TextBlock{Text: ckptRandomASCII(r, 30)}},
		}
	}
	return msgs
}

// ckptRandomSnapshot generates a random Snapshot with random fields.
func ckptRandomSnapshot(r *rand.Rand) *agent.Snapshot {
	return &agent.Snapshot{
		ID:       ckptRandomASCII(r, 15),
		Node:     ckptRandomASCII(r, 10),
		NextNode: ckptRandomASCII(r, 10),
		State:    ckptRandomState(r),
		History:  ckptRandomHistory(r),
	}
}

// ckptSnapshotsEqual compares two snapshots for deep equality.
func ckptSnapshotsEqual(a, b *agent.Snapshot) bool {
	if a.ID != b.ID || a.Node != b.Node || a.NextNode != b.NextNode {
		return false
	}
	if !reflect.DeepEqual(a.State, b.State) {
		return false
	}
	if len(a.History) != len(b.History) {
		return false
	}
	for i := range a.History {
		if a.History[i].Role != b.History[i].Role {
			return false
		}
		if len(a.History[i].Content) != len(b.History[i].Content) {
			return false
		}
		for j := range a.History[i].Content {
			if !reflect.DeepEqual(a.History[i].Content[j], b.History[i].Content[j]) {
				return false
			}
		}
	}
	return true
}

// Feature: advanced-orchestration, Property 4: Snapshot round-trip
// **Validates: Requirements 2.3, 2.6**

// TestProperty_SnapshotRoundTrip verifies that for any valid Snapshot,
// Save followed by Load returns an equivalent Snapshot.
func TestProperty_SnapshotRoundTrip(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := agent.NewInMemoryCheckpointStore()

		snap := ckptRandomSnapshot(r)
		id := ckptRandomASCII(r, 15)

		if err := store.Save(ctx, id, snap); err != nil {
			t.Logf("Save failed: %v", err)
			return false
		}

		loaded, err := store.Load(ctx, id)
		if err != nil {
			t.Logf("Load failed: %v", err)
			return false
		}

		if !ckptSnapshotsEqual(snap, loaded) {
			t.Logf("Round-trip mismatch: saved %+v, loaded %+v", snap, loaded)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Snapshot round-trip — failed: %v", err)
	}
}

// TestProperty_ErrSnapshotNotFoundForMissingIDs verifies that Load returns
// agent.ErrSnapshotNotFound for any ID that was never saved.
func TestProperty_ErrSnapshotNotFoundForMissingIDs(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := agent.NewInMemoryCheckpointStore()

		// Generate a random ID that was never saved.
		id := ckptRandomASCII(r, 20)

		_, err := store.Load(ctx, id)
		if err == nil {
			t.Logf("expected error for missing ID %q, got nil", id)
			return false
		}
		if !errors.Is(err, agent.ErrSnapshotNotFound) {
			t.Logf("expected ErrSnapshotNotFound, got: %v", err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: ErrSnapshotNotFound for missing IDs — failed: %v", err)
	}
}

// TestProperty_DeleteRemovesSnapshot verifies that after saving a Snapshot
// and then deleting it, Load returns ErrSnapshotNotFound.
func TestProperty_DeleteRemovesSnapshot(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		store := agent.NewInMemoryCheckpointStore()

		snap := ckptRandomSnapshot(r)
		id := ckptRandomASCII(r, 15)

		// Save a snapshot.
		if err := store.Save(ctx, id, snap); err != nil {
			t.Logf("Save failed: %v", err)
			return false
		}

		// Verify it exists.
		_, err := store.Load(ctx, id)
		if err != nil {
			t.Logf("Load after Save failed: %v", err)
			return false
		}

		// Delete it.
		if err := store.Delete(ctx, id); err != nil {
			t.Logf("Delete failed: %v", err)
			return false
		}

		// Verify it's gone.
		_, err = store.Load(ctx, id)
		if err == nil {
			t.Logf("expected ErrSnapshotNotFound after Delete for ID %q, got nil", id)
			return false
		}
		if !errors.Is(err, agent.ErrSnapshotNotFound) {
			t.Logf("expected ErrSnapshotNotFound after Delete, got: %v", err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Delete removes snapshot — failed: %v", err)
	}
}

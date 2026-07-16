package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
)

// testCheckpointStore is a thread-safe in-memory CheckpointStore for tests.
type testCheckpointStore struct {
	mu        sync.RWMutex
	snapshots map[string]*agent.Snapshot
}

func newTestCheckpointStore() *testCheckpointStore {
	return &testCheckpointStore{snapshots: make(map[string]*agent.Snapshot)}
}

func (s *testCheckpointStore) Save(_ context.Context, id string, snap *agent.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[id] = snap
	return nil
}

func (s *testCheckpointStore) Load(_ context.Context, id string) (*agent.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, agent.ErrSnapshotNotFound
	}
	return snap, nil
}

func (s *testCheckpointStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, id)
	return nil
}

func TestGraph_ResumeFrom_RestoresAndContinues(t *testing.T) {
	store := newTestCheckpointStore()
	state := agent.NewMapState()

	g := agent.NewGraph("a", agent.GraphOptions{
		State:           state,
		CheckpointStore: store,
		CheckpointID:    "test-checkpoint",
	})

	g.AddNode("a", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			s.Set("step", "a-done")
			return nil
		},
	})
	g.AddNode("b", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("from-b")}})
	g.AddNode("c", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("from-c")}})

	g.AddEdge("a", "b")
	g.AddEdge("b", "c")

	// Pre-seed the store with a snapshot as if node "a" completed and "b" is next.
	if err := store.Save(context.Background(), "test-checkpoint", &agent.Snapshot{
		ID:       "test-checkpoint",
		Node:     "a",
		NextNode: "b",
		State:    map[string]any{"step": "a-done"},
		History: []bond.Message{
			{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Resume from the checkpoint — should execute "b" then "c".
	var text string
	for event, err := range g.ResumeFrom(context.Background(), "test-checkpoint") {
		if err != nil {
			t.Fatalf("ResumeFrom: %v", err)
		}
		if event.Type == bond.StreamEventTextDelta {
			text += event.TextDelta
		}
	}

	// "b" output followed by "c" output.
	if text != "from-bfrom-c" {
		t.Errorf("expected 'from-bfrom-c', got %q", text)
	}

	// State should be restored.
	val, ok := state.Get("step")
	if !ok || val != "a-done" {
		t.Errorf("expected state[step]='a-done', got %v", val)
	}

	// Checkpoint should be cleaned up after normal completion.
	_, err := store.Load(context.Background(), "test-checkpoint")
	if !errors.Is(err, agent.ErrSnapshotNotFound) {
		t.Error("expected checkpoint to be deleted after completion")
	}
}

func TestGraph_ResumeFrom_SnapshotNotFound(t *testing.T) {
	store := newTestCheckpointStore()

	g := agent.NewGraph("a", agent.GraphOptions{
		CheckpointStore: store,
		CheckpointID:    "test-checkpoint",
	})
	g.AddNode("a", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("hi")}})

	var gotErr error
	for _, err := range g.ResumeFrom(context.Background(), "nonexistent") {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for missing snapshot")
	}
	if !errors.Is(gotErr, agent.ErrSnapshotNotFound) {
		t.Errorf("expected ErrSnapshotNotFound in chain, got: %v", gotErr)
	}
}

func TestGraph_ResumeFrom_NoCheckpointStore(t *testing.T) {
	g := agent.NewGraph("a", agent.GraphOptions{})
	g.AddNode("a", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("hi")}})

	var gotErr error
	for _, err := range g.ResumeFrom(context.Background(), "any-id") {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error when checkpoint store not configured")
	}
}

func TestGraph_ResumeFrom_StateRestored(t *testing.T) {
	store := newTestCheckpointStore()
	state := agent.NewMapState()

	g := agent.NewGraph("a", agent.GraphOptions{
		State:           state,
		CheckpointStore: store,
		CheckpointID:    "ckpt",
	})

	// Node "b" reads state set during the resume.
	g.AddNode("a", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("a")}})
	g.AddNode("b", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			val, ok := s.Get("restored_key")
			if !ok || val != "restored_value" {
				return errors.New("state not properly restored")
			}
			s.Set("verified", true)
			return nil
		},
	})

	g.AddEdge("a", "b")

	// Snapshot: node "a" completed, "b" is next, with state to restore.
	if err := store.Save(context.Background(), "ckpt", &agent.Snapshot{
		ID:       "ckpt",
		Node:     "a",
		NextNode: "b",
		State:    map[string]any{"restored_key": "restored_value"},
		History:  []bond.Message{},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, err := range g.ResumeFrom(context.Background(), "ckpt") {
		if err != nil {
			t.Fatalf("ResumeFrom: %v", err)
		}
	}

	val, ok := state.Get("verified")
	if !ok || val != true {
		t.Error("expected state[verified]=true after action node verified restoration")
	}
}

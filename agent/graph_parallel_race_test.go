package agent_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
)

// TestGraph_ParallelRace is an aggressive race-detector test that runs
// many parallel action nodes concurrently with heavy read/write contention
// on shared state. It exercises:
// - Each node writing unique keys (write path)
// - Each node reading Keys() (read path)
// - Each node reading Get() on its own key (read path)
// - Cross-node reads where node A reads a key set by node B
//
// The goal is to trigger data races if the State mutex is insufficient.
// Run with: go test ./agent/... -race -run TestGraph_ParallelRace -count=5
//
// **Validates: Requirements 4.6**
func TestGraph_ParallelRace(t *testing.T) {
	const numNodes = 20

	// Build node names.
	names := make([]string, numNodes)
	for i := range names {
		names[i] = fmt.Sprintf("worker_%02d", i)
	}

	state := agent.NewMapState()
	g := agent.NewGraph("start", agent.GraphOptions{State: state})

	g.AddNode("start", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			// Seed initial keys so cross-reads have something to find.
			for i := 0; i < numNodes; i++ {
				s.Set(fmt.Sprintf("seed_%02d", i), i)
			}
			return nil
		},
	})

	// Each parallel node performs a mix of reads and writes to maximize contention.
	for i, name := range names {
		nodeIdx := i
		nodeName := name
		g.AddNode(nodeName, &agent.GraphNode{
			Action: func(ctx context.Context, s agent.State) error {
				// Write a unique key (write path).
				s.Set("key_"+nodeName, nodeName+"_value")

				// Read all keys — exercises RLock on the full map (read path).
				keys := s.Keys()
				_ = keys

				// Read own key back (read path).
				_, _ = s.Get("key_" + nodeName)

				// Cross-reads: read keys written by other nodes. The values may
				// or may not be present depending on goroutine scheduling, but
				// access must be race-free regardless.
				for j := 0; j < 5; j++ {
					crossIdx := (nodeIdx + j + 1) % numNodes
					crossKey := fmt.Sprintf("key_worker_%02d", crossIdx)
					_, _ = s.Get(crossKey)
				}

				// Read seed keys set during the start node (guaranteed present).
				for j := 0; j < 3; j++ {
					seedIdx := (nodeIdx + j) % numNodes
					_, _ = s.Get(fmt.Sprintf("seed_%02d", seedIdx))
				}

				// Write additional keys to increase contention.
				s.Set(fmt.Sprintf("extra_%s_a", nodeName), "a")
				s.Set(fmt.Sprintf("extra_%s_b", nodeName), "b")

				// Final read of Keys() after writes.
				_ = s.Keys()

				return nil
			},
		})
	}

	g.AddNode("join", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error { return nil },
	})

	g.FanOutEdge("start", names, "join")

	// Run the graph.
	_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	// Verify all primary keys were written.
	for _, name := range names {
		key := "key_" + name
		v, ok := state.Get(key)
		if !ok {
			t.Errorf("state key %q missing after parallel execution", key)
			continue
		}
		expected := name + "_value"
		if v != expected {
			t.Errorf("state[%q] = %v, want %q", key, v, expected)
		}
	}

	// Verify extra keys were written.
	for _, name := range names {
		for _, suffix := range []string{"a", "b"} {
			key := fmt.Sprintf("extra_%s_%s", name, suffix)
			if _, ok := state.Get(key); !ok {
				t.Errorf("state key %q missing after parallel execution", key)
			}
		}
	}
}

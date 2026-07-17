package agent_test

import (
	"context"
	"fmt"
	"iter"
	"math/rand"
	"sort"
	"sync/atomic"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
)

// Feature: advanced-orchestration, Property 3: Parallel node output is deterministic
// **Validates: Requirements 4.5, 4.6, 4.8**

// TestProperty_ParallelOutputDeterministic verifies that for any set of parallel
// nodes producing output, the appended history order is sorted by node name
// regardless of goroutine completion order. The graph is run multiple times with
// the same input and the join node always receives history in the same order.
func TestProperty_ParallelOutputDeterministic(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))

		// Generate 2–6 random node names.
		numNodes := 2 + r.Intn(5)
		names := generateUniqueNames(r, numNodes)

		// Expected order after sorting by name.
		sortedNames := make([]string, len(names))
		copy(sortedNames, names)
		sort.Strings(sortedNames)

		// Run the graph multiple times and verify deterministic output.
		const runs = 3
		var firstRunOrder []string

		for run := 0; run < runs; run++ {
			g := agent.NewGraph("start", agent.GraphOptions{})

			g.AddNode("start", &agent.GraphNode{
				Action: func(ctx context.Context, s agent.State) error { return nil },
			})

			// Add parallel agent nodes that each produce output matching their name.
			for _, name := range names {
				nodeName := name
				g.AddNode(nodeName, &agent.GraphNode{
					Agent: &bondtest.Agent{Events: bondtest.TextEvents("output_" + nodeName)},
				})
			}

			// Capture messages received by the join node.
			var joinMessages []bond.Message
			g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{
				StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
					joinMessages = append([]bond.Message{}, messages...)
					return bondtest.Repeat(bondtest.TextEvents("done"))(ctx, messages)
				},
			}})

			g.FanOutEdge("start", names, "join")

			_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
			if err != nil {
				t.Logf("run %d: Invoke failed: %v", run, err)
				return false
			}

			// Extract parallel output messages (skip the initial user message).
			if len(joinMessages) < 1+numNodes {
				t.Logf("run %d: expected at least %d messages, got %d", run, 1+numNodes, len(joinMessages))
				return false
			}

			// Messages[0] is user prompt; messages[1..numNodes] are parallel outputs.
			order := make([]string, numNodes)
			for i := 0; i < numNodes; i++ {
				text := extractText(joinMessages[i+1])
				order[i] = text
			}

			// Verify sorted order.
			for i, name := range sortedNames {
				expected := "output_" + name
				if order[i] != expected {
					t.Logf("run %d: position %d expected %q, got %q", run, i, expected, order[i])
					return false
				}
			}

			// Verify consistency across runs.
			if run == 0 {
				firstRunOrder = order
			} else {
				for i := range order {
					if order[i] != firstRunOrder[i] {
						t.Logf("run %d: output order differs from run 0 at position %d: %q vs %q",
							run, i, order[i], firstRunOrder[i])
						return false
					}
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Parallel node output is deterministic — failed: %v", err)
	}
}

// TestProperty_ParallelErrorCancellation verifies that when one parallel branch
// returns an error, the whole graph returns an error (errgroup cancels siblings).
// **Validates: Requirements 4.5**
func TestProperty_ParallelErrorCancellation(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))

		// Generate 2–5 node names; pick one at random to fail.
		numNodes := 2 + r.Intn(4)
		names := generateUniqueNames(r, numNodes)
		failIdx := r.Intn(numNodes)

		g := agent.NewGraph("start", agent.GraphOptions{})
		g.AddNode("start", &agent.GraphNode{
			Action: func(ctx context.Context, s agent.State) error { return nil },
		})

		for i, name := range names {
			nodeName := name
			shouldFail := i == failIdx
			g.AddNode(nodeName, &agent.GraphNode{
				Action: func(ctx context.Context, s agent.State) error {
					if shouldFail {
						return fmt.Errorf("intentional failure in %s", nodeName)
					}
					return nil
				},
			})
		}

		g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("joined")}})
		g.FanOutEdge("start", names, "join")

		_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
		if err == nil {
			t.Logf("expected error from parallel branch failure, got nil")
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Parallel error cancellation — failed: %v", err)
	}
}

// TestProperty_ParallelHookFiring verifies that BeforeNodeTransitionHook and
// AfterNodeExecutionHook fire for each parallel target node.
// **Validates: Requirements 4.9, 4.10**
func TestProperty_ParallelHookFiring(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))

		// Generate 2–5 parallel nodes.
		numNodes := 2 + r.Intn(4)
		names := generateUniqueNames(r, numNodes)

		g := agent.NewGraph("start", agent.GraphOptions{})
		g.AddNode("start", &agent.GraphNode{
			Action: func(ctx context.Context, s agent.State) error { return nil },
		})

		for _, name := range names {
			nodeName := name
			g.AddNode(nodeName, &agent.GraphNode{
				Action: func(ctx context.Context, s agent.State) error { return nil },
			})
		}
		g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("done")}})
		g.FanOutEdge("start", names, "join")

		// Track hook invocations.
		var beforeCount atomic.Int32
		var afterCount atomic.Int32

		registry := &bond.HookRegistry{}
		bond.OnBefore(registry, func(ctx context.Context, event *agent.BeforeNodeTransitionHook) error {
			beforeCount.Add(1)
			return nil
		})
		bond.OnAfter(registry, func(ctx context.Context, event *agent.AfterNodeExecutionHook) {
			afterCount.Add(1)
		})

		ctx := bond.WithHookRegistry(context.Background(), registry)
		for _, err := range g.Stream(ctx, bond.TextPrompt("go")) {
			if err != nil {
				t.Logf("Stream error: %v", err)
				return false
			}
		}

		// BeforeNodeTransition fires for: start + each parallel node + join = 1 + numNodes + 1
		expectedBefore := int32(1 + numNodes + 1)
		if got := beforeCount.Load(); got != expectedBefore {
			t.Logf("expected %d BeforeNodeTransition hooks, got %d", expectedBefore, got)
			return false
		}

		// AfterNodeExecution fires for: start + each parallel node + join = 1 + numNodes + 1
		expectedAfter := int32(1 + numNodes + 1)
		if got := afterCount.Load(); got != expectedAfter {
			t.Logf("expected %d AfterNodeExecution hooks, got %d", expectedAfter, got)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Parallel hook firing — failed: %v", err)
	}
}

// TestProperty_ParallelSharedStateMutations verifies that concurrent state
// mutations from parallel action nodes are safe under the race detector.
// Each parallel node writes a unique key; after completion all keys are present.
// **Validates: Requirements 4.6**
func TestProperty_ParallelSharedStateMutations(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))

		// Generate 3–8 parallel action nodes that each set a unique key.
		numNodes := 3 + r.Intn(6)
		names := generateUniqueNames(r, numNodes)

		state := agent.NewMapState()
		g := agent.NewGraph("start", agent.GraphOptions{State: state})

		g.AddNode("start", &agent.GraphNode{
			Action: func(ctx context.Context, s agent.State) error { return nil },
		})

		for _, name := range names {
			nodeName := name
			g.AddNode(nodeName, &agent.GraphNode{
				Action: func(ctx context.Context, s agent.State) error {
					// Write a unique key to state concurrently.
					s.Set("key_"+nodeName, nodeName+"_value")
					// Also read state to exercise RLock path.
					_ = s.Keys()
					return nil
				},
			})
		}

		g.AddNode("join", &agent.GraphNode{
			Action: func(ctx context.Context, s agent.State) error { return nil },
		})
		g.FanOutEdge("start", names, "join")

		_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
		if err != nil {
			t.Logf("Invoke error: %v", err)
			return false
		}

		// Verify all keys were written successfully.
		for _, name := range names {
			v, ok := state.Get("key_" + name)
			if !ok {
				t.Logf("state key %q missing", "key_"+name)
				return false
			}
			if v != name+"_value" {
				t.Logf("state[%q] = %v, want %q", "key_"+name, v, name+"_value")
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Parallel shared state mutations — failed: %v", err)
	}
}

// generateUniqueNames produces n unique lowercase alphabetic names of length 3–6.
func generateUniqueNames(r *rand.Rand, n int) []string {
	seen := make(map[string]bool, n)
	names := make([]string, 0, n)
	for len(names) < n {
		length := 3 + r.Intn(4)
		b := make([]byte, length)
		for i := range b {
			b[i] = 'a' + byte(r.Intn(26))
		}
		name := string(b)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

package agent_test

import (
	"context"
	"fmt"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
)

func TestGraph_FanOutEdge_BasicFlow(t *testing.T) {
	g := agent.NewGraph("start", agent.GraphOptions{})

	g.AddNode("start", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("start")}})
	g.AddNode("branch_a", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("from_a")}})
	g.AddNode("branch_b", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("from_b")}})
	g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("joined")}})

	g.FanOutEdge("start", []string{"branch_b", "branch_a"}, "join")

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// bond.Invoke accumulates all text deltas, so start + join.
	expected := "startjoined"
	if resp.Text != expected {
		t.Errorf("expected %q, got %q", expected, resp.Text)
	}
}

func TestGraph_FanOutEdge_DeterministicOrder(t *testing.T) {
	// Verify that parallel node outputs are appended to history in sorted order.
	g := agent.NewGraph("start", agent.GraphOptions{})

	g.AddNode("start", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			return nil
		},
	})
	g.AddNode("alpha", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("output_alpha")}})
	g.AddNode("beta", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("output_beta")}})
	g.AddNode("gamma", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("output_gamma")}})

	// The join node captures the history it receives via a StreamFunc.
	var joinMessages []bond.Message
	g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			joinMessages = messages
			return bondtest.Repeat(bondtest.TextEvents("done"))(ctx, messages)
		},
	}})

	g.FanOutEdge("start", []string{"gamma", "alpha", "beta"}, "join")

	_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// History should have: [user:go, assistant:output_alpha, assistant:output_beta, assistant:output_gamma]
	// Check sorted order of the parallel outputs.
	if len(joinMessages) < 4 {
		t.Fatalf("expected at least 4 messages in join history, got %d", len(joinMessages))
	}

	// Messages at indices 1, 2, 3 should be the parallel outputs in sorted order.
	expectedOrder := []string{"output_alpha", "output_beta", "output_gamma"}
	for i, expected := range expectedOrder {
		msg := joinMessages[i+1]
		text := extractText(msg)
		if text != expected {
			t.Errorf("message[%d]: expected %q, got %q", i+1, expected, text)
		}
	}
}

func TestGraph_FanOutEdge_ActionNodes(t *testing.T) {
	state := agent.NewMapState()
	g := agent.NewGraph("start", agent.GraphOptions{State: state})

	g.AddNode("start", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			return nil
		},
	})
	g.AddNode("action_a", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			s.Set("a_done", true)
			return nil
		},
	})
	g.AddNode("action_b", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			s.Set("b_done", true)
			return nil
		},
	})
	g.AddNode("end", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("complete")}})

	g.FanOutEdge("start", []string{"action_a", "action_b"}, "end")

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "complete" {
		t.Errorf("expected 'complete', got %q", resp.Text)
	}

	// Both actions should have executed.
	if v, ok := state.Get("a_done"); !ok || v != true {
		t.Error("expected state 'a_done' to be true")
	}
	if v, ok := state.Get("b_done"); !ok || v != true {
		t.Error("expected state 'b_done' to be true")
	}
}

func TestGraph_FanOutEdge_ErrorCancels(t *testing.T) {
	g := agent.NewGraph("start", agent.GraphOptions{})

	g.AddNode("start", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			return nil
		},
	})
	g.AddNode("good", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			return nil
		},
	})
	g.AddNode("bad", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			return fmt.Errorf("intentional failure")
		},
	})
	g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("joined")}})

	g.FanOutEdge("start", []string{"good", "bad"}, "join")

	_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err == nil {
		t.Fatal("expected error from parallel branch failure")
	}
}

func TestGraph_FanOutEdge_HooksFire(t *testing.T) {
	g := agent.NewGraph("start", agent.GraphOptions{})

	g.AddNode("start", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error { return nil },
	})
	g.AddNode("branch_a", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error { return nil },
	})
	g.AddNode("branch_b", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error { return nil },
	})
	g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("done")}})

	g.FanOutEdge("start", []string{"branch_a", "branch_b"}, "join")

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

	// Call g.Stream directly with hook-attached context (like ResumeFrom pattern).
	ctx := bond.WithHookRegistry(context.Background(), registry)
	for _, err := range g.Stream(ctx, bond.TextPrompt("go")) {
		if err != nil {
			t.Fatalf("Stream error: %v", err)
		}
	}

	// BeforeNodeTransition fires for: start, branch_a, branch_b, join = 4
	if got := beforeCount.Load(); got != 4 {
		t.Errorf("expected 4 BeforeNodeTransition hooks, got %d", got)
	}

	// AfterNodeExecution fires for: start, branch_a, branch_b, join = 4
	if got := afterCount.Load(); got != 4 {
		t.Errorf("expected 4 AfterNodeExecution hooks, got %d", got)
	}
}

func TestGraph_FanOutEdge_ReceivesFullHistory(t *testing.T) {
	// Verify parallel nodes each receive the full conversation history at fan-out point.
	g := agent.NewGraph("start", agent.GraphOptions{})

	g.AddNode("start", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("start_output")}})

	var branchAMessages []bond.Message
	var branchBMessages []bond.Message

	g.AddNode("branch_a", &agent.GraphNode{Agent: &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			branchAMessages = append([]bond.Message{}, messages...)
			return bondtest.Repeat(bondtest.TextEvents("a_out"))(ctx, messages)
		},
	}})
	g.AddNode("branch_b", &agent.GraphNode{Agent: &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			branchBMessages = append([]bond.Message{}, messages...)
			return bondtest.Repeat(bondtest.TextEvents("b_out"))(ctx, messages)
		},
	}})
	g.AddNode("join", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("joined")}})

	g.FanOutEdge("start", []string{"branch_a", "branch_b"}, "join")

	_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("hello"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// Both branches should see: [user:hello, assistant:start_output]
	if len(branchAMessages) != 2 {
		t.Errorf("branch_a expected 2 messages, got %d", len(branchAMessages))
	}
	if len(branchBMessages) != 2 {
		t.Errorf("branch_b expected 2 messages, got %d", len(branchBMessages))
	}
}

// extractText pulls the text from a bond.Message.
func extractText(msg bond.Message) string {
	var text string
	for _, block := range msg.Content {
		if tb, ok := block.(*bond.TextBlock); ok {
			text += tb.Text
		}
	}
	return text
}

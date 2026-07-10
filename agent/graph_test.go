package agent_test

import (
	"context"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
)

func TestGraph_SimpleLinearFlow(t *testing.T) {
	g := agent.NewGraph("a", agent.GraphOptions{})

	g.AddNode("a", &agent.GraphNode{Agent: &bondtest.EchoAgent{}})
	g.AddNode("b", &agent.GraphNode{Agent: &bondtest.EchoAgent{}})

	g.AddEdge("a", "b")

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("hello"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// Echo agent echoes last message — after node "a" runs, "b" sees "a"'s output.
	if resp.Text == "" {
		t.Error("expected non-empty response")
	}
}

func TestGraph_ConditionalEdge(t *testing.T) {
	state := agent.NewMapState()

	g := agent.NewGraph("start", agent.GraphOptions{State: state})

	g.AddNode("start", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			s.Set("route", "b")
			return nil
		},
	})
	g.AddNode("a", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("went to a")}})
	g.AddNode("b", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("went to b")}})

	g.AddConditionalEdge("start", func(s agent.State) string {
		route, _ := s.Get("route")
		if route == "b" {
			return "b"
		}
		return "a"
	})

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "went to b" {
		t.Errorf("expected 'went to b', got %q", resp.Text)
	}
}

func TestGraph_ActionNode(t *testing.T) {
	state := agent.NewMapState()

	g := agent.NewGraph("action", agent.GraphOptions{State: state})

	g.AddNode("action", &agent.GraphNode{
		Action: func(ctx context.Context, s agent.State) error {
			s.Set("prepared", true)
			return nil
		},
	})
	g.AddNode("agent", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("done")}})

	g.AddEdge("action", "agent")

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "done" {
		t.Errorf("expected 'done', got %q", resp.Text)
	}

	val, ok := state.Get("prepared")
	if !ok || val != true {
		t.Error("expected state 'prepared' to be true")
	}
}

func TestGraph_NoEdgesTerminates(t *testing.T) {
	g := agent.NewGraph("only", agent.GraphOptions{})
	g.AddNode("only", &agent.GraphNode{Agent: &bondtest.Agent{Events: bondtest.TextEvents("solo")}})

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "solo" {
		t.Errorf("expected 'solo', got %q", resp.Text)
	}
}

func TestGraph_UnknownNodeErrors(t *testing.T) {
	g := agent.NewGraph("missing", agent.GraphOptions{})

	_, err := bond.Invoke(context.Background(), g, bond.TextPrompt("go"), bond.AgentOptions{})
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
}

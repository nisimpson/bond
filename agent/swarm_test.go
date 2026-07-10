package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
)

func TestSwarm_NoTransfer(t *testing.T) {
	s := agent.NewSwarm("handler", agent.SwarmOptions{})

	s.AddAgent("handler", &agent.SwarmAgent{
		Agent:       &bondtest.Agent{Events: bondtest.TextEvents("handled it")},
		Description: "Handles everything.",
	})

	resp, err := bond.Invoke(context.Background(), s, bond.TextPrompt("do it"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "handled it" {
		t.Errorf("expected 'handled it', got %q", resp.Text)
	}
}

func TestSwarm_SingleTransfer(t *testing.T) {
	s := agent.NewSwarm("dispatch", agent.SwarmOptions{})

	// Dispatch agent calls transfer_to_specialist.
	s.AddAgent("dispatch", &agent.SwarmAgent{
		Agent: &bondtest.Agent{
			StreamFunc: bondtest.Sequence(
				[]bond.StreamEvent{
					{Type: bond.StreamEventStart},
					{Type: bond.StreamEventToolUse, ToolUse: &bond.ToolUseBlock{
						ID:    "t1",
						Name:  "transfer_to_specialist",
						Input: json.RawMessage(`{}`),
					}},
					{Type: bond.StreamEventStop, StopReason: bond.StopReasonToolUse},
				},
				bondtest.TextEvents("fallback"),
			),
		},
		Description: "Dispatches to specialists.",
	})

	s.AddAgent("specialist", &agent.SwarmAgent{
		Agent:       &bondtest.Agent{Events: bondtest.TextEvents("specialist here")},
		Description: "Handles specialized tasks.",
	})

	resp, err := bond.Invoke(context.Background(), s, bond.TextPrompt("need help"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text == "" {
		t.Error("expected non-empty response from specialist")
	}
	if !contains(resp.Text, "specialist here") {
		t.Errorf("expected response to contain 'specialist here', got %q", resp.Text)
	}
}

func TestSwarm_MaxHandoffs(t *testing.T) {
	s := agent.NewSwarm("a", agent.SwarmOptions{MaxHandoffs: 2})

	// Each agent transfers to the other. Without MaxHandoffs this would loop forever.
	// Use Sequence: first call = transfer, second call (after tool result) = end.
	s.AddAgent("a", &agent.SwarmAgent{
		Agent: &bondtest.Agent{
			StreamFunc: bondtest.Sequence(
				[]bond.StreamEvent{
					{Type: bond.StreamEventStart},
					{Type: bond.StreamEventToolUse, ToolUse: &bond.ToolUseBlock{
						ID: "t1", Name: "transfer_to_b", Input: json.RawMessage(`{}`),
					}},
					{Type: bond.StreamEventStop, StopReason: bond.StopReasonToolUse},
				},
				bondtest.TextEvents("a-done"),
			),
		},
		Description: "Agent A",
	})
	s.AddAgent("b", &agent.SwarmAgent{
		Agent: &bondtest.Agent{
			StreamFunc: bondtest.Sequence(
				[]bond.StreamEvent{
					{Type: bond.StreamEventStart},
					{Type: bond.StreamEventToolUse, ToolUse: &bond.ToolUseBlock{
						ID: "t1", Name: "transfer_to_a", Input: json.RawMessage(`{}`),
					}},
					{Type: bond.StreamEventStop, StopReason: bond.StopReasonToolUse},
				},
				bondtest.TextEvents("b-done"),
			),
		},
		Description: "Agent B",
	})

	resp, err := bond.Invoke(context.Background(), s, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// Should terminate — the exact output depends on which agent was active when limit hit.
	if resp.Text == "" {
		t.Error("expected non-empty response after max handoffs")
	}
}

func TestSwarm_SharedState(t *testing.T) {
	state := agent.NewMapState()
	s := agent.NewSwarm("writer", agent.SwarmOptions{State: state})

	// Writer agent uses write_state tool.
	s.AddAgent("writer", &agent.SwarmAgent{
		Agent: &bondtest.Agent{
			StreamFunc: bondtest.Sequence(
				bondtest.ToolUseEvents(&bond.ToolUseBlock{
					ID:    "t1",
					Name:  "write_state",
					Input: json.RawMessage(`{"key":"mission","value":"accomplished"}`),
				}),
				bondtest.TextEvents("state written"),
			),
		},
		Description: "Writes state.",
	})

	resp, err := bond.Invoke(context.Background(), s, bond.TextPrompt("go"), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Text != "state written" {
		t.Errorf("expected 'state written', got %q", resp.Text)
	}

	val, ok := state.Get("mission")
	if !ok || val != "accomplished" {
		t.Errorf("expected state mission=accomplished, got %v", val)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

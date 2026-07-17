package agent_test

import (
	"context"
	"encoding/json"
	"iter"
	"math/rand"
	"testing"
	"testing/quick"

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

// randomASCII generates a random ASCII string of the given length.
func randomASCII(rng *rand.Rand, length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = byte(32 + rng.Intn(95)) // printable ASCII
	}
	return string(b)
}

// generateMessages creates a random conversation of 1-10 messages.
func generateMessages(rng *rand.Rand) []bond.Message {
	count := 1 + rng.Intn(10)
	msgs := make([]bond.Message, count)
	for i := range msgs {
		role := bond.RoleUser
		if i%2 == 1 {
			role = bond.RoleAssistant
		}
		msgs[i] = bond.Message{
			Role:    role,
			Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 1+rng.Intn(20))}},
		}
	}
	return msgs
}

// tailPolicy is a HistoryPolicy that keeps only the last N messages.
type tailPolicy struct {
	n int
}

func (p *tailPolicy) Select(_ context.Context, messages []bond.Message) ([]bond.Message, error) {
	if len(messages) <= p.n {
		return messages, nil
	}
	return messages[len(messages)-p.n:], nil
}

// capturingAgent records the messages it receives during Stream.
type capturingAgent struct {
	received []bond.Message
}

func (a *capturingAgent) Stream(_ context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	a.received = append(a.received[:0], messages...)
	return func(yield func(bond.StreamEvent, error) bool) {
		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}
		if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: "ok"}, nil) {
			return
		}
		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// Feature: advanced-orchestration, Property 7: Swarm full history preserved internally
// Validates: Requirements 5.8, 5.9
func TestProperty_SwarmNilPolicyPassthroughFullHistory(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		messages := generateMessages(rng)

		capture := &capturingAgent{}
		s := agent.NewSwarm("handler", agent.SwarmOptions{})
		s.AddAgent("handler", &agent.SwarmAgent{
			Agent:         capture,
			Description:   "Handles everything.",
			HistoryPolicy: nil, // nil = full history passthrough
		})

		_, err := bond.Invoke(context.Background(), s, messages, bond.AgentOptions{})
		if err != nil {
			t.Logf("Invoke error: %v", err)
			return false
		}

		// Property: with nil policy, agent receives the full input messages.
		if len(capture.received) != len(messages) {
			t.Logf("expected %d messages, got %d", len(messages), len(capture.received))
			return false
		}
		for i := range messages {
			expectedText := bondtest.TextFromBlock(messages[i])
			receivedText := bondtest.TextFromBlock(capture.received[i])
			if expectedText != receivedText {
				t.Logf("message[%d] mismatch: got %q, want %q", i, receivedText, expectedText)
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Swarm nil policy passthrough (full history) — failed: %v", err)
	}
}

// Feature: advanced-orchestration, Property 7: Swarm full history preserved internally
// Validates: Requirements 5.8, 5.9
func TestProperty_SwarmFilteredPolicySubsetReceivedByAgent(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		messages := generateMessages(rng)

		// Use a tail policy that keeps at most 2 messages.
		policy := &tailPolicy{n: 2}
		capture := &capturingAgent{}

		s := agent.NewSwarm("handler", agent.SwarmOptions{})
		s.AddAgent("handler", &agent.SwarmAgent{
			Agent:         capture,
			Description:   "Handles with filter.",
			HistoryPolicy: policy,
		})

		_, err := bond.Invoke(context.Background(), s, messages, bond.AgentOptions{})
		if err != nil {
			t.Logf("Invoke error: %v", err)
			return false
		}

		// Property: agent receives at most policy.n messages.
		if len(capture.received) > policy.n {
			t.Logf("expected at most %d messages, got %d", policy.n, len(capture.received))
			return false
		}

		// Property: received messages are a suffix of the original input.
		expectedStart := len(messages) - len(capture.received)
		for i, msg := range capture.received {
			expectedText := bondtest.TextFromBlock(messages[expectedStart+i])
			receivedText := bondtest.TextFromBlock(msg)
			if expectedText != receivedText {
				t.Logf("filtered message[%d] mismatch: got %q, want %q", i, receivedText, expectedText)
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Swarm filtered policy (subset received by agent) — failed: %v", err)
	}
}

// Feature: advanced-orchestration, Property 7: Swarm full history preserved internally
// Validates: Requirements 5.9
func TestProperty_SwarmInternalHistoryPreservedAfterTransfer(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		messages := generateMessages(rng)

		// The specialist has a tail policy keeping only 2 messages.
		// After transfer, the specialist should see a filtered view,
		// but the swarm's internal history has the full conversation
		// (original messages + dispatch assistant output).
		specialistCapture := &capturingAgent{}

		s := agent.NewSwarm("dispatch", agent.SwarmOptions{})

		// Dispatch agent transfers to specialist. The first call emits a tool use
		// (transfer), and the second call (after bond.Stream processes the tool)
		// emits "done" text. The combined output appended to swarm internal history
		// is the assistant message with that text.
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
					bondtest.TextEvents("done"),
				),
			},
			Description:   "Dispatches work.",
			HistoryPolicy: nil,
		})

		s.AddAgent("specialist", &agent.SwarmAgent{
			Agent:         specialistCapture,
			Description:   "Specialist with filter.",
			HistoryPolicy: &tailPolicy{n: 2},
		})

		_, err := bond.Invoke(context.Background(), s, messages, bond.AgentOptions{})
		if err != nil {
			t.Logf("Invoke error: %v", err)
			return false
		}

		// After dispatch runs, swarm internal history = original messages + assistant("done").
		// Total internal history length = len(messages) + 1.
		// The specialist's tailPolicy{n:2} keeps only the last 2 messages.
		// So specialist receives at most 2 messages.
		if len(specialistCapture.received) > 2 {
			t.Logf("specialist expected at most 2 messages, got %d", len(specialistCapture.received))
			return false
		}

		// Property: the specialist does NOT see the full internal history.
		// The swarm's internal history has len(messages)+1 entries.
		// If original messages had 2+ entries, the specialist must have fewer.
		internalHistoryLen := len(messages) + 1
		if internalHistoryLen > 2 && len(specialistCapture.received) >= internalHistoryLen {
			t.Logf("specialist should see filtered history (%d), not full (%d)",
				len(specialistCapture.received), internalHistoryLen)
			return false
		}

		// Property: the specialist's received messages are the tail of the
		// full internal history, confirming internal history preserved all messages.
		// The last message in internal history is the dispatch's "done" output.
		if len(specialistCapture.received) > 0 {
			lastReceived := bondtest.TextFromBlock(specialistCapture.received[len(specialistCapture.received)-1])
			if lastReceived != "done" {
				t.Logf("last message should be dispatch output 'done', got %q", lastReceived)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Swarm internal history preserved after transfer — failed: %v", err)
	}
}

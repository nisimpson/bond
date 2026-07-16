package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
	"github.com/nisimpson/bond/extra/session"
)

// TestSwarm_HistoryPolicy_Integration exercises the Swarm HistoryPolicy
// integration with a real SlidingWindowManager from extra/session.
//
// Scenario:
//  1. Swarm with 2 agents: "dispatcher" and "specialist".
//  2. Dispatcher produces multiple assistant messages, then transfers to specialist.
//  3. Specialist has SlidingWindowManager(windowSize=2) as HistoryPolicy.
//  4. Verify: specialist receives at most the last 2 user/assistant pairs.
//  5. Verify: the swarm's internal history contains all messages (unfiltered).
//
// Validates: Requirements 5.7, 5.8, 5.9
func TestSwarm_HistoryPolicy_Integration(t *testing.T) {
	// --- Build a SlidingWindowManager with window size 2 ---
	policy, err := session.NewSlidingWindowManager(session.SlidingWindowOptions{
		WindowSize: 2,
	})
	if err != nil {
		t.Fatalf("NewSlidingWindowManager: %v", err)
	}

	// --- Build a capturing specialist agent that records received messages ---
	specialist := &historyCapturingAgent{}

	// --- Build the dispatcher agent ---
	// The dispatcher emits a transfer tool call on first invocation, then on the
	// second invocation (after bond.Stream processes the tool result) produces text.
	// This mirrors the pattern in TestSwarm_SingleTransfer.
	dispatcher := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			// First call: emit text + transfer tool use.
			[]bond.StreamEvent{
				{Type: bond.StreamEventStart},
				{Type: bond.StreamEventTextDelta, TextDelta: "dispatching now"},
				{Type: bond.StreamEventToolUse, ToolUse: &bond.ToolUseBlock{
					ID:    "t1",
					Name:  "transfer_to_specialist",
					Input: json.RawMessage(`{}`),
				}},
				{Type: bond.StreamEventStop, StopReason: bond.StopReasonToolUse},
			},
			// Second call (after tool result): end turn.
			bondtest.TextEvents("dispatcher done"),
		),
	}

	// --- Configure the Swarm ---
	s := agent.NewSwarm("dispatcher", agent.SwarmOptions{})

	s.AddAgent("dispatcher", &agent.SwarmAgent{
		Agent:       dispatcher,
		Description: "Routes conversations to specialists.",
	})

	s.AddAgent("specialist", &agent.SwarmAgent{
		Agent:         specialist,
		Description:   "Handles specialized tasks with limited context.",
		HistoryPolicy: policy,
	})

	// --- Build initial conversation with 5 user/assistant pairs ---
	// This simulates a long conversation that happened before this invocation.
	initialMessages := buildConversation(5)

	// --- Invoke the swarm ---
	_, err = bond.Invoke(context.Background(), s, initialMessages, bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// --- Verify: specialist received filtered history ---
	// The swarm's internal history at transfer time is:
	//   initialMessages (10 msgs: 5 user/assistant pairs)
	//   + dispatcher's assistant output (1 msg: "dispatching nowdispatcher done")
	//   = 11 messages total, with 5 user messages.
	//
	// SlidingWindowManager(windowSize=2) keeps the last 2 user/assistant pairs.
	// userIndices in the 11-message history are at positions [0,2,4,6,8].
	// cutIndex = userIndices[5-2] = userIndices[3] = 6
	// So specialist receives messages[6:11] = 5 messages (user4, asst4, user5, asst5, dispatcher_asst).

	// Count user messages received by the specialist.
	var userMsgCount int
	for _, msg := range specialist.received {
		if msg.Role == bond.RoleUser {
			userMsgCount++
		}
	}

	if userMsgCount > 2 {
		t.Errorf("specialist received %d user messages, expected at most 2 (window size)", userMsgCount)
	}
	if userMsgCount != 2 {
		t.Errorf("specialist received %d user messages, expected exactly 2", userMsgCount)
	}

	// The specialist should NOT have the full 11-message history.
	internalHistoryLen := len(initialMessages) + 1 // +1 for dispatcher's assistant message
	if len(specialist.received) >= internalHistoryLen {
		t.Errorf("specialist received %d messages (full history=%d); policy should have filtered",
			len(specialist.received), internalHistoryLen)
	}

	// --- Verify: specialist received some messages (policy didn't drop everything) ---
	if len(specialist.received) == 0 {
		t.Fatal("specialist received 0 messages; expected filtered subset")
	}

	// --- Verify: swarm internal history is complete ---
	// The last message in the specialist's view should include the dispatcher's
	// assistant output, confirming the swarm accumulated it into internal history
	// before applying the filter.
	lastSpecialistMsg := textFromBlock(specialist.received[len(specialist.received)-1])
	if lastSpecialistMsg != "dispatching nowdispatcher done" {
		t.Errorf("expected specialist's last message to be dispatcher output, got %q", lastSpecialistMsg)
	}

	// --- Verify: the specialist's first user message is NOT "user message 1" ---
	// This confirms older messages were filtered out.
	firstUserMsg := ""
	for _, msg := range specialist.received {
		if msg.Role == bond.RoleUser {
			firstUserMsg = textFromBlock(msg)
			break
		}
	}
	if firstUserMsg == "user message 1" {
		t.Error("specialist should not see 'user message 1' — it should be filtered out")
	}

	t.Logf("Specialist received %d messages (filtered from %d internal history messages)",
		len(specialist.received), internalHistoryLen)
}

// historyCapturingAgent records the messages it receives and responds with simple text.
type historyCapturingAgent struct {
	received []bond.Message
}

func (a *historyCapturingAgent) Stream(_ context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	a.received = make([]bond.Message, len(messages))
	copy(a.received, messages)

	return func(yield func(bond.StreamEvent, error) bool) {
		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}
		if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: "specialist handled"}, nil) {
			return
		}
		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// buildConversation creates n user/assistant pairs as a message slice.
func buildConversation(pairs int) []bond.Message {
	msgs := make([]bond.Message, 0, pairs*2)
	for i := 1; i <= pairs; i++ {
		msgs = append(msgs,
			bond.Message{
				Role:    bond.RoleUser,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("user message %d", i)}},
			},
			bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("assistant reply %d", i)}},
			},
		)
	}
	return msgs
}

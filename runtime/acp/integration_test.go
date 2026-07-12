package acp

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

// integrationHelper encapsulates common io.Pipe-based test infrastructure.
type integrationHelper struct {
	t            *testing.T
	handler      *Handler
	clientReader *io.PipeReader
	clientWriter *io.PipeWriter
	msgCh        chan Message
	serveWg      sync.WaitGroup
	cancel       context.CancelFunc
}

func newIntegrationHelper(t *testing.T, agent bond.Agent, opts Options) *integrationHelper {
	t.Helper()

	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	transport := NewTransport(serverReader, serverWriter)
	opts.Transport = transport

	h := NewHandler(agent, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	ih := &integrationHelper{
		t:            t,
		handler:      h,
		clientReader: clientReader,
		clientWriter: clientWriter,
		msgCh:        make(chan Message, 200),
		cancel:       cancel,
	}

	// Start reading messages from the server.
	go func() {
		buf := make([]byte, 65536)
		var partial string
		for {
			n, err := clientReader.Read(buf)
			if err != nil {
				return
			}
			partial += string(buf[:n])
			lines := strings.Split(partial, "\n")
			partial = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				if line == "" {
					continue
				}
				var msg Message
				if json.Unmarshal([]byte(line), &msg) == nil {
					ih.msgCh <- msg
				}
			}
		}
	}()

	// Start serving.
	ih.serveWg.Add(1)
	go func() {
		defer ih.serveWg.Done()
		_ = h.Serve(ctx)
	}()

	return ih
}

func (ih *integrationHelper) send(msg string) {
	ih.t.Helper()
	_, err := ih.clientWriter.Write([]byte(msg + "\n"))
	if err != nil {
		ih.t.Fatalf("failed to send: %v", err)
	}
}

func (ih *integrationHelper) sendJSON(msg Message) {
	ih.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		ih.t.Fatalf("failed to marshal: %v", err)
	}
	ih.send(string(data))
}

func (ih *integrationHelper) waitForMsg(predicate func(Message) bool, timeout time.Duration) (Message, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ih.msgCh:
			if predicate(msg) {
				return msg, true
			}
		case <-deadline:
			return Message{}, false
		}
	}
}

func (ih *integrationHelper) waitForResponse(id string, timeout time.Duration) (Message, bool) {
	return ih.waitForMsg(func(m Message) bool {
		return m.ID != nil && string(*m.ID) == id
	}, timeout)
}

// collectUntilResponse collects messages until a response with the given ID arrives.
// Returns the response and all intermediate messages.
func (ih *integrationHelper) collectUntilResponse(id string, timeout time.Duration) (Message, []Message) {
	var collected []Message
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ih.msgCh:
			if msg.ID != nil && string(*msg.ID) == id && (msg.Result != nil || msg.Error != nil) {
				return msg, collected
			}
			collected = append(collected, msg)
		case <-deadline:
			ih.t.Fatalf("timed out waiting for response with id %q", id)
			return Message{}, nil
		}
	}
}

func (ih *integrationHelper) initialize() {
	ih.t.Helper()
	ih.send(`{"jsonrpc":"2.0","method":"initialize","id":"init","params":{"protocolVersion":1}}`)
	resp, ok := ih.waitForResponse(`"init"`, 2*time.Second)
	if !ok {
		ih.t.Fatal("timed out waiting for initialize response")
	}
	if resp.Error != nil {
		ih.t.Fatalf("initialize failed: %s", resp.Error.Message)
	}
}

func (ih *integrationHelper) createSession() {
	ih.t.Helper()
	ih.send(`{"jsonrpc":"2.0","method":"session/new","id":"sess","params":{"cwd":"/workspace"}}`)
	resp, ok := ih.waitForResponse(`"sess"`, 2*time.Second)
	if !ok {
		ih.t.Fatal("timed out waiting for session/new response")
	}
	if resp.Error != nil {
		ih.t.Fatalf("session/new failed: %s", resp.Error.Message)
	}
	// Drain available_commands notification.
	ih.waitForMsg(func(m Message) bool {
		if m.Method != "session/update" || m.Params == nil {
			return false
		}
		var notif struct{ Type string }
		_ = json.Unmarshal(m.Params, &notif)
		return notif.Type == "available_commands"
	}, 2*time.Second)
}

func (ih *integrationHelper) close() {
	ih.clientWriter.Close()
	ih.serveWg.Wait()
	ih.cancel()
}

// --- Integration Tests ---

func TestIntegration_FullPromptRoundTrip(t *testing.T) {
	// Tests: initialize → session/new → session/prompt → text delta notifications → response
	// Validates: Requirements 4.1, 4.2, 4.3

	agent := &bondtest.Agent{
		Events: []bond.StreamEvent{
			{Type: bond.StreamEventStart},
			{Type: bond.StreamEventTextDelta, TextDelta: "Hello, "},
			{Type: bond.StreamEventTextDelta, TextDelta: "world!"},
			{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd},
		},
	}

	ih := newIntegrationHelper(t, agent, Options{
		AgentName:    "integration-agent",
		AgentVersion: "2.0.0",
	})
	defer ih.close()

	// Step 1: Initialize
	ih.initialize()

	// Step 2: Create session
	ih.createSession()

	// Step 3: Send prompt
	ih.send(`{"jsonrpc":"2.0","method":"session/prompt","id":"p1","params":{"message":"say hello"}}`)

	// Step 4: Collect messages until prompt response
	promptResp, notifications := ih.collectUntilResponse(`"p1"`, 5*time.Second)

	// Verify text delta notifications were sent
	var deltas []string
	var messageIDs []string
	for _, msg := range notifications {
		if msg.Method == "session/update" && msg.Params != nil {
			var notif struct {
				Type      string `json:"type"`
				MessageID string `json:"messageId"`
				Delta     string `json:"delta"`
			}
			if json.Unmarshal(msg.Params, &notif) == nil && notif.Type == "agent_message_chunk" {
				deltas = append(deltas, notif.Delta)
				messageIDs = append(messageIDs, notif.MessageID)
			}
		}
	}

	if len(deltas) != 2 {
		t.Fatalf("expected 2 text delta notifications, got %d", len(deltas))
	}
	if deltas[0] != "Hello, " || deltas[1] != "world!" {
		t.Errorf("unexpected deltas: %v", deltas)
	}

	// All messageIDs should be stable (same) within the turn
	if messageIDs[0] != messageIDs[1] {
		t.Errorf("messageIds should be stable within a turn: got %q and %q", messageIDs[0], messageIDs[1])
	}
	if messageIDs[0] == "" {
		t.Error("messageId should not be empty")
	}

	// Verify final response
	if promptResp.Error != nil {
		t.Fatalf("unexpected error: %s", promptResp.Error.Message)
	}
	var result struct {
		StopReason string `json:"stop_reason"`
	}
	_ = json.Unmarshal(promptResp.Result, &result)
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}
}

func TestIntegration_ToolCallPermissionSelected(t *testing.T) {
	// Tests: full tool call lifecycle with permission "selected" flow
	// Validates: Requirements 5.1, 5.2, 5.3, 7.1, 7.2

	greetTool, err := bond.NewFuncTool(
		func(ctx context.Context, input struct{ Name string }) (string, error) {
			return "hi " + input.Name, nil
		},
		bond.FuncToolOptions{
			Name:        "greet",
			Description: "Greets someone",
		},
	)
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	toolUseBlock := &bond.ToolUseBlock{
		ID:    "tc-integ-1",
		Name:  "greet",
		Input: json.RawMessage(`{"Name":"Alice"}`),
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(toolUseBlock),
			bondtest.TextEvents("greeting sent"),
		),
	}

	ih := newIntegrationHelper(t, agent, Options{
		AgentName:    "tool-agent",
		AgentVersion: "1.0.0",
		AgentOptions: bond.AgentOptions{
			Tools: []bond.Tool{greetTool},
		},
	})
	defer ih.close()

	ih.initialize()
	ih.createSession()

	// Send prompt that triggers tool call
	ih.send(`{"jsonrpc":"2.0","method":"session/prompt","id":"p1","params":{"message":"greet alice"}}`)

	// Collect messages until we see the permission request, saving tool notifications along the way.
	var prePermNotifs []Message
	var permMsg Message
	deadline := time.After(5 * time.Second)
waitPerm:
	for {
		select {
		case msg := <-ih.msgCh:
			if msg.Method == "session/request_permission" && msg.ID != nil {
				permMsg = msg
				break waitPerm
			}
			prePermNotifs = append(prePermNotifs, msg)
		case <-deadline:
			t.Fatal("timed out waiting for session/request_permission")
		}
	}

	// Verify permission request content
	var permParams struct {
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Input      json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(permMsg.Params, &permParams)
	if permParams.ToolCallID != "tc-integ-1" {
		t.Errorf("expected toolCallId 'tc-integ-1', got %q", permParams.ToolCallID)
	}
	if permParams.ToolName != "greet" {
		t.Errorf("expected toolName 'greet', got %q", permParams.ToolName)
	}

	// Respond with "selected" — approve the tool
	ih.sendJSON(Message{
		JSONRPC: "2.0",
		ID:      permMsg.ID,
		Result:  json.RawMessage(`{"outcome":"selected"}`),
	})

	// Collect until prompt response
	promptResp, postPermCollected := ih.collectUntilResponse(`"p1"`, 5*time.Second)

	// Merge pre-permission and post-permission messages for analysis
	collected := append(prePermNotifs, postPermCollected...)

	// Verify tool lifecycle notifications
	type toolNotif struct {
		Type       string `json:"type"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Status     string `json:"status"`
		Result     string `json:"result"`
	}
	var toolNotifs []toolNotif
	for _, msg := range collected {
		if msg.Method == "session/update" && msg.Params != nil {
			var n toolNotif
			if json.Unmarshal(msg.Params, &n) == nil && (n.Type == "tool_call" || n.Type == "tool_call_update") {
				toolNotifs = append(toolNotifs, n)
			}
		}
	}

	// Expect: pending → in_progress → completed
	if len(toolNotifs) < 3 {
		t.Fatalf("expected at least 3 tool notifications, got %d: %+v", len(toolNotifs), toolNotifs)
	}

	if toolNotifs[0].Type != "tool_call" || toolNotifs[0].Status != "pending" {
		t.Errorf("expected tool_call/pending, got %s/%s", toolNotifs[0].Type, toolNotifs[0].Status)
	}
	if toolNotifs[1].Type != "tool_call_update" || toolNotifs[1].Status != "in_progress" {
		t.Errorf("expected tool_call_update/in_progress, got %s/%s", toolNotifs[1].Type, toolNotifs[1].Status)
	}
	if toolNotifs[2].Type != "tool_call_update" || toolNotifs[2].Status != "completed" {
		t.Errorf("expected tool_call_update/completed, got %s/%s", toolNotifs[2].Type, toolNotifs[2].Status)
	}
	if !strings.Contains(toolNotifs[2].Result, "hi Alice") {
		t.Errorf("expected result to contain 'hi Alice', got %q", toolNotifs[2].Result)
	}

	// Verify prompt response
	if promptResp.Error != nil {
		t.Fatalf("unexpected error: %s", promptResp.Error.Message)
	}
	var result struct {
		StopReason string `json:"stop_reason"`
	}
	_ = json.Unmarshal(promptResp.Result, &result)
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}
}

func TestIntegration_ToolCallPermissionCancelled(t *testing.T) {
	// Tests: tool call with permission "cancelled" — tool should NOT execute
	// Validates: Requirements 7.3

	toolExecuted := false
	greetTool, err := bond.NewFuncTool(
		func(ctx context.Context, input struct{ Name string }) (string, error) {
			toolExecuted = true
			return "hi " + input.Name, nil
		},
		bond.FuncToolOptions{
			Name:        "greet",
			Description: "Greets someone",
		},
	)
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	toolUseBlock := &bond.ToolUseBlock{
		ID:    "tc-integ-2",
		Name:  "greet",
		Input: json.RawMessage(`{"Name":"Bob"}`),
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(toolUseBlock),
			bondtest.TextEvents("after cancel"),
		),
	}

	ih := newIntegrationHelper(t, agent, Options{
		AgentName:    "tool-agent",
		AgentVersion: "1.0.0",
		AgentOptions: bond.AgentOptions{
			Tools: []bond.Tool{greetTool},
		},
	})
	defer ih.close()

	ih.initialize()
	ih.createSession()

	// Send prompt
	ih.send(`{"jsonrpc":"2.0","method":"session/prompt","id":"p1","params":{"message":"greet bob"}}`)

	// Wait for permission request
	permMsg, ok := ih.waitForMsg(func(m Message) bool {
		return m.Method == "session/request_permission" && m.ID != nil
	}, 5*time.Second)
	if !ok {
		t.Fatal("timed out waiting for session/request_permission")
	}

	// Respond with "cancelled" — deny the tool
	ih.sendJSON(Message{
		JSONRPC: "2.0",
		ID:      permMsg.ID,
		Result:  json.RawMessage(`{"outcome":"cancelled"}`),
	})

	// Collect until prompt response
	promptResp, collected := ih.collectUntilResponse(`"p1"`, 5*time.Second)

	// Verify tool was NOT executed
	if toolExecuted {
		t.Error("tool should NOT have been executed when permission is cancelled")
	}

	// Verify no "completed" tool notification was sent
	for _, msg := range collected {
		if msg.Method == "session/update" && msg.Params != nil {
			var notif struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			}
			if json.Unmarshal(msg.Params, &notif) == nil && notif.Type == "tool_call_update" && notif.Status == "completed" {
				t.Error("should NOT have a tool_call_update with status 'completed'")
			}
		}
	}

	// Verify prompt still completes with end_turn
	if promptResp.Error != nil {
		t.Fatalf("unexpected error: %s", promptResp.Error.Message)
	}
	var result struct {
		StopReason string `json:"stop_reason"`
	}
	_ = json.Unmarshal(promptResp.Result, &result)
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}
}

func TestIntegration_CancelDuringActivePrompt(t *testing.T) {
	// Tests: cancel during active prompt → stop_reason "cancelled"
	// Validates: Requirements 6.1, 6.2

	// Agent that blocks until context is cancelled.
	agent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			return func(yield func(bond.StreamEvent, error) bool) {
				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				// Emit one delta then block
				if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: "thinking..."}, nil) {
					return
				}
				<-ctx.Done() // Block until cancelled
			}
		},
	}

	ih := newIntegrationHelper(t, agent, Options{
		AgentName:    "blocking-agent",
		AgentVersion: "1.0.0",
	})
	defer ih.close()

	ih.initialize()
	ih.createSession()

	// Send prompt (will block)
	ih.send(`{"jsonrpc":"2.0","method":"session/prompt","id":"p1","params":{"message":"think hard"}}`)

	// Wait a bit for the agent to start streaming
	time.Sleep(100 * time.Millisecond)

	// Send cancel notification
	ih.send(`{"jsonrpc":"2.0","method":"session/cancel"}`)

	// Wait for prompt response
	promptResp, _ := ih.collectUntilResponse(`"p1"`, 5*time.Second)

	// Verify stop_reason is "cancelled"
	if promptResp.Error != nil {
		t.Fatalf("unexpected error: %s", promptResp.Error.Message)
	}
	var result struct {
		StopReason string `json:"stop_reason"`
	}
	_ = json.Unmarshal(promptResp.Result, &result)
	if result.StopReason != "cancelled" {
		t.Errorf("expected stop_reason 'cancelled', got %q", result.StopReason)
	}

	// Verify session is still active
	ih.handler.mu.Lock()
	session := ih.handler.session
	ih.handler.mu.Unlock()
	if session == nil {
		t.Fatal("session should still be active after cancellation")
	}
}

func TestIntegration_MultipleSequentialPrompts(t *testing.T) {
	// Tests: multiple sequential prompts accumulating history
	// Validates: Requirements 4.1, 4.7

	// Track call count to verify history grows with each call.
	var callMu sync.Mutex
	var historySizes []int

	agent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			callMu.Lock()
			historySizes = append(historySizes, len(messages))
			callMu.Unlock()

			// Echo back the last user message
			text := ""
			if len(messages) > 0 {
				last := messages[len(messages)-1]
				for _, b := range last.Content {
					if tb, ok := b.(*bond.TextBlock); ok {
						text += tb.Text
					}
				}
			}

			return func(yield func(bond.StreamEvent, error) bool) {
				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				if text != "" {
					if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: "echo: " + text}, nil) {
						return
					}
				}
				yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
			}
		},
	}

	ih := newIntegrationHelper(t, agent, Options{
		AgentName:    "history-agent",
		AgentVersion: "1.0.0",
	})
	defer ih.close()

	ih.initialize()
	ih.createSession()

	// Send 3 sequential prompts
	prompts := []struct {
		id      string
		message string
	}{
		{id: `"p1"`, message: "first"},
		{id: `"p2"`, message: "second"},
		{id: `"p3"`, message: "third"},
	}

	for _, p := range prompts {
		ih.send(`{"jsonrpc":"2.0","method":"session/prompt","id":` + p.id + `,"params":{"message":"` + p.message + `"}}`)
		resp, _ := ih.collectUntilResponse(p.id, 5*time.Second)
		if resp.Error != nil {
			t.Fatalf("prompt %s failed: %s", p.id, resp.Error.Message)
		}
		var result struct {
			StopReason string `json:"stop_reason"`
		}
		json.Unmarshal(resp.Result, &result) //nolint:errcheck
		if result.StopReason != "end_turn" {
			t.Errorf("prompt %s: expected stop_reason 'end_turn', got %q", p.id, result.StopReason)
		}
	}

	// Verify history accumulated correctly
	ih.handler.mu.Lock()
	session := ih.handler.session
	ih.handler.mu.Unlock()

	if session == nil {
		t.Fatal("session should exist")
	}

	// After 3 prompts, history should have 6 messages (3 user + 3 assistant)
	if len(session.History) != 6 {
		t.Fatalf("expected 6 messages in history, got %d", len(session.History))
	}

	// Verify alternating user/assistant pattern
	for i, msg := range session.History {
		expectedRole := bond.RoleUser
		if i%2 == 1 {
			expectedRole = bond.RoleAssistant
		}
		if msg.Role != expectedRole {
			t.Errorf("history[%d]: expected role %q, got %q", i, expectedRole, msg.Role)
		}
	}

	// Verify the agent received growing history on each call
	callMu.Lock()
	sizes := historySizes
	callMu.Unlock()

	if len(sizes) != 3 {
		t.Fatalf("expected 3 agent calls, got %d", len(sizes))
	}
	// First call: 1 message (user), Second: 3 (user+assistant+user), Third: 5
	expectedSizes := []int{1, 3, 5}
	for i, expected := range expectedSizes {
		if sizes[i] != expected {
			t.Errorf("call %d: expected %d messages passed to agent, got %d", i+1, expected, sizes[i])
		}
	}

	// Verify content of user messages
	for i, p := range prompts {
		userMsg := session.History[i*2]
		if len(userMsg.Content) == 0 {
			t.Fatalf("history[%d]: expected content", i*2)
		}
		tb, ok := userMsg.Content[0].(*bond.TextBlock)
		if !ok {
			t.Fatalf("history[%d]: expected TextBlock", i*2)
		}
		if tb.Text != p.message {
			t.Errorf("history[%d]: expected %q, got %q", i*2, p.message, tb.Text)
		}
	}
}

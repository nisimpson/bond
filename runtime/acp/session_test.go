package acp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nisimpson/bond/bondtest"
)

func TestNewSession(t *testing.T) {
	s := newSession("test-id", "/home/user/project")
	if s.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", s.ID)
	}
	if s.CWD != "/home/user/project" {
		t.Errorf("expected CWD '/home/user/project', got %q", s.CWD)
	}
	if len(s.History) != 0 {
		t.Errorf("expected empty history, got %d messages", len(s.History))
	}
	if s.promptActive {
		t.Error("expected promptActive to be false")
	}
	if s.promptCancel != nil {
		t.Error("expected promptCancel to be nil")
	}
}

func TestSession_Close(t *testing.T) {
	s := newSession("test-id", "/tmp")

	// Set up a cancel func to verify it gets called.
	_, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.promptCancel = cancel
	s.promptActive = true
	s.mu.Unlock()

	s.close()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promptCancel != nil {
		t.Error("expected promptCancel to be nil after close")
	}
	if s.promptActive {
		t.Error("expected promptActive to be false after close")
	}
}

func newTestHandlerWithCommands(input string, commands []Command) (*Handler, *strings.Builder) {
	out := &strings.Builder{}
	transport := NewTransport(strings.NewReader(input), out)
	agent := &bondtest.EchoAgent{}
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
		Commands:     commands,
	})
	return h, out
}

func TestHandleSessionNew_HappyPath(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/home/user/project"}}` + "\n"

	commands := []Command{
		{Name: "/fix", Description: "Fix issues"},
		{Name: "/explain", Description: "Explain code"},
	}
	h, out := newTestHandlerWithCommands(input, commands)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expect: initialize response, session/new response, session/update notification
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %v", len(lines), lines)
	}

	// Verify session/new response.
	var resp Message
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatalf("failed to parse session/new response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("expected success, got error: %v", resp.Error)
	}

	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected non-empty session_id")
	}

	// Verify session stored on handler.
	h.mu.Lock()
	session := h.session
	h.mu.Unlock()

	if session == nil {
		t.Fatal("expected session to be stored on handler")
	}
	if session.ID != result.SessionID {
		t.Errorf("session ID mismatch: handler has %q, response has %q", session.ID, result.SessionID)
	}
	if session.CWD != "/home/user/project" {
		t.Errorf("expected CWD '/home/user/project', got %q", session.CWD)
	}

	// Verify session/update notification (available_commands).
	var notif Message
	if err := json.Unmarshal([]byte(lines[2]), &notif); err != nil {
		t.Fatalf("failed to parse notification: %v", err)
	}
	if notif.Method != "session/update" {
		t.Errorf("expected method 'session/update', got %q", notif.Method)
	}
	if notif.ID != nil {
		t.Error("notification should not have an id")
	}

	var notifParams struct {
		SessionID string    `json:"sessionId"`
		Type      string    `json:"type"`
		Commands  []Command `json:"commands"`
	}
	if err := json.Unmarshal(notif.Params, &notifParams); err != nil {
		t.Fatalf("failed to parse notification params: %v", err)
	}
	if notifParams.SessionID != result.SessionID {
		t.Errorf("notification sessionId mismatch: got %q, want %q", notifParams.SessionID, result.SessionID)
	}
	if notifParams.Type != "available_commands" {
		t.Errorf("expected type 'available_commands', got %q", notifParams.Type)
	}
	if len(notifParams.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(notifParams.Commands))
	}
	if notifParams.Commands[0].Name != "/fix" {
		t.Errorf("expected first command '/fix', got %q", notifParams.Commands[0].Name)
	}
	if notifParams.Commands[1].Name != "/explain" {
		t.Errorf("expected second command '/explain', got %q", notifParams.Commands[1].Name)
	}
}

func TestHandleSessionNew_Replacement(t *testing.T) {
	// Send two session/new requests — the second should replace the first.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/first"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":3,"params":{"cwd":"/second"}}` + "\n"

	h, out := newTestHandlerWithCommands(input, nil)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expect: init response, session/new #1 response, notification #1, session/new #2 response, notification #2
	if len(lines) != 5 {
		t.Fatalf("expected 5 output lines, got %d: %v", len(lines), lines)
	}

	// Parse first session response.
	var resp1 Message
	if err := json.Unmarshal([]byte(lines[1]), &resp1); err != nil {
		t.Fatalf("failed to parse first session response: %v", err)
	}
	var result1 struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(resp1.Result, &result1); err != nil {
		t.Fatalf("failed to parse first session result: %v", err)
	}

	// Parse second session response.
	var resp2 Message
	if err := json.Unmarshal([]byte(lines[3]), &resp2); err != nil {
		t.Fatalf("failed to parse second session response: %v", err)
	}
	if resp2.Error != nil {
		t.Fatalf("expected second session/new to succeed, got error: %v", resp2.Error)
	}
	var result2 struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(resp2.Result, &result2); err != nil {
		t.Fatalf("failed to parse second session result: %v", err)
	}

	// Session IDs should be different.
	if result1.SessionID == result2.SessionID {
		t.Errorf("expected different session IDs, both are %q", result1.SessionID)
	}

	// Handler should hold the second session.
	h.mu.Lock()
	session := h.session
	h.mu.Unlock()

	if session == nil {
		t.Fatal("expected session to be stored")
	}
	if session.ID != result2.SessionID {
		t.Errorf("expected handler to hold second session %q, got %q", result2.SessionID, session.ID)
	}
	if session.CWD != "/second" {
		t.Errorf("expected CWD '/second', got %q", session.CWD)
	}
}

func TestHandleSessionNew_AvailableCommandsNotificationSent(t *testing.T) {
	// Verify notification is sent even with empty commands list.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n"

	h, out := newTestHandlerWithCommands(input, nil)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %v", len(lines), lines)
	}

	// Third line should be the notification.
	var notif Message
	if err := json.Unmarshal([]byte(lines[2]), &notif); err != nil {
		t.Fatalf("failed to parse notification: %v", err)
	}
	if notif.Method != "session/update" {
		t.Errorf("expected method 'session/update', got %q", notif.Method)
	}

	var params struct {
		SessionID string    `json:"sessionId"`
		Type      string    `json:"type"`
		Commands  []Command `json:"commands"`
	}
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("failed to parse notification params: %v", err)
	}
	if params.Type != "available_commands" {
		t.Errorf("expected type 'available_commands', got %q", params.Type)
	}
	// Empty commands should serialize as an empty array, not null.
	if params.Commands == nil {
		t.Error("expected commands to be empty array, got nil")
	}
	if len(params.Commands) != 0 {
		t.Errorf("expected 0 commands, got %d", len(params.Commands))
	}
}

func TestHandleSessionNew_ReplacementCancelsActivePrompt(t *testing.T) {
	// Simulate a session with an active prompt, then replace it.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/first"}}` + "\n"

	h, _ := newTestHandlerWithCommands(input, nil)
	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// Manually set up an active prompt on the first session.
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	firstSession := h.session
	h.mu.Unlock()

	firstSession.mu.Lock()
	firstSession.promptActive = true
	firstSession.promptCancel = cancel
	firstSession.mu.Unlock()

	// Now send a second session/new.
	input2 := `{"jsonrpc":"2.0","method":"session/new","id":3,"params":{"cwd":"/second"}}` + "\n"
	out2 := &strings.Builder{}
	h.transport = NewTransport(strings.NewReader(input2), out2)

	err = h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// The first session's context should have been cancelled.
	if ctx.Err() == nil {
		t.Error("expected first session's prompt context to be cancelled")
	}

	// Handler should now hold the new session.
	h.mu.Lock()
	session := h.session
	h.mu.Unlock()
	if session.CWD != "/second" {
		t.Errorf("expected CWD '/second', got %q", session.CWD)
	}
}

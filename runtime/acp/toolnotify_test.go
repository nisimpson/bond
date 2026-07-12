package acp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

func TestToolCallNotifications_Success(t *testing.T) {
	// Test: A tool call that succeeds should produce:
	// 1. tool_call notification (status: "pending")
	// 2. tool_call_update notification (status: "in_progress")
	// 3. tool_call_update notification (status: "completed", with result)

	greetTool, err := bond.NewFuncTool(
		func(ctx context.Context, input struct{ Name string }) (string, error) {
			return "hello " + input.Name, nil
		},
		bond.FuncToolOptions{
			Name:        "greet",
			Description: "Greets a person",
		},
	)
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	toolUseBlock := &bond.ToolUseBlock{
		ID:    "tool-call-123",
		Name:  "greet",
		Input: json.RawMessage(`{"Name":"World"}`),
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(toolUseBlock),
			bondtest.TextEvents("done"),
		),
	}

	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	transport := NewTransport(serverReader, serverWriter)
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
		AgentOptions: bond.AgentOptions{
			Tools: []bond.Tool{greetTool},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgCh := make(chan Message, 100)
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
					msgCh <- msg
				}
			}
		}
	}()

	var serveWg sync.WaitGroup
	serveWg.Add(1)
	go func() {
		defer serveWg.Done()
		_ = h.Serve(ctx)
	}()

	send := func(msg string) {
		_, _ = clientWriter.Write([]byte(msg + "\n"))
	}

	waitForMsg := func(predicate func(Message) bool, timeout time.Duration) (Message, bool) {
		deadline := time.After(timeout)
		for {
			select {
			case msg := <-msgCh:
				if predicate(msg) {
					return msg, true
				}
			case <-deadline:
				return Message{}, false
			}
		}
	}

	// Initialize.
	send(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}`)
	waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "1" }, 2*time.Second)

	// Create session.
	send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
	waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "2" }, 2*time.Second)
	waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 2*time.Second)

	// Send prompt.
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"greet world"}}`)

	// Collect all messages until the prompt response, auto-approving permission requests.
	type toolNotif struct {
		Type       string `json:"type"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Status     string `json:"status"`
		Result     string `json:"result"`
		Error      string `json:"error"`
	}

	var toolNotifs []toolNotif
	deadline := time.After(5 * time.Second)
waitForPromptResp:
	for {
		select {
		case msg := <-msgCh:
			// Auto-approve permission requests inline.
			if msg.Method == "session/request_permission" && msg.ID != nil {
				permResp := Message{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  json.RawMessage(`{"outcome":"selected"}`),
				}
				data, _ := json.Marshal(permResp)
				send(string(data))
				continue
			}
			if msg.ID != nil && string(*msg.ID) == "3" && msg.Result != nil {
				break waitForPromptResp
			}
			if msg.Method == "session/update" && msg.Params != nil {
				var notif toolNotif
				if json.Unmarshal(msg.Params, &notif) == nil && (notif.Type == "tool_call" || notif.Type == "tool_call_update") {
					toolNotifs = append(toolNotifs, notif)
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for prompt response")
		}
	}

	// Expect exactly 3 tool notifications: pending, in_progress, completed.
	if len(toolNotifs) != 3 {
		t.Fatalf("expected 3 tool notifications, got %d: %+v", len(toolNotifs), toolNotifs)
	}

	if toolNotifs[0].Type != "tool_call" || toolNotifs[0].Status != "pending" {
		t.Errorf("notification 1: expected tool_call/pending, got %s/%s", toolNotifs[0].Type, toolNotifs[0].Status)
	}
	if toolNotifs[0].ToolCallID != "tool-call-123" {
		t.Errorf("notification 1: expected toolCallId 'tool-call-123', got %q", toolNotifs[0].ToolCallID)
	}
	if toolNotifs[0].ToolName != "greet" {
		t.Errorf("notification 1: expected toolName 'greet', got %q", toolNotifs[0].ToolName)
	}

	if toolNotifs[1].Type != "tool_call_update" || toolNotifs[1].Status != "in_progress" {
		t.Errorf("notification 2: expected tool_call_update/in_progress, got %s/%s", toolNotifs[1].Type, toolNotifs[1].Status)
	}
	if toolNotifs[1].ToolCallID != "tool-call-123" {
		t.Errorf("notification 2: expected toolCallId 'tool-call-123', got %q", toolNotifs[1].ToolCallID)
	}

	if toolNotifs[2].Type != "tool_call_update" || toolNotifs[2].Status != "completed" {
		t.Errorf("notification 3: expected tool_call_update/completed, got %s/%s", toolNotifs[2].Type, toolNotifs[2].Status)
	}
	if toolNotifs[2].ToolCallID != "tool-call-123" {
		t.Errorf("notification 3: expected toolCallId 'tool-call-123', got %q", toolNotifs[2].ToolCallID)
	}
	if toolNotifs[2].Result == "" {
		t.Error("notification 3: expected non-empty result")
	}
	if !strings.Contains(toolNotifs[2].Result, "hello World") {
		t.Errorf("notification 3: expected result to contain 'hello World', got %q", toolNotifs[2].Result)
	}

	clientWriter.Close()
	serveWg.Wait()
}

func TestToolCallNotifications_Failure(t *testing.T) {
	// Test: A tool call that fails (unknown tool) should produce:
	// 1. tool_call notification (status: "pending")
	// 2. tool_call_update notification (status: "in_progress")
	// 3. tool_call_update notification (status: "failed", with error message)

	toolUseBlock := &bond.ToolUseBlock{
		ID:    "tool-call-456",
		Name:  "nonexistent_tool",
		Input: json.RawMessage(`{}`),
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(toolUseBlock),
			bondtest.TextEvents("done"),
		),
	}

	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	transport := NewTransport(serverReader, serverWriter)
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgCh := make(chan Message, 100)
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
					msgCh <- msg
				}
			}
		}
	}()

	var serveWg sync.WaitGroup
	serveWg.Add(1)
	go func() {
		defer serveWg.Done()
		_ = h.Serve(ctx)
	}()

	send := func(msg string) {
		_, _ = clientWriter.Write([]byte(msg + "\n"))
	}

	waitForMsg := func(predicate func(Message) bool, timeout time.Duration) (Message, bool) {
		deadline := time.After(timeout)
		for {
			select {
			case msg := <-msgCh:
				if predicate(msg) {
					return msg, true
				}
			case <-deadline:
				return Message{}, false
			}
		}
	}

	// Initialize.
	send(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}`)
	waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "1" }, 2*time.Second)

	// Create session.
	send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
	waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "2" }, 2*time.Second)
	waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 2*time.Second)

	// Send prompt.
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"call tool"}}`)

	// Collect all messages until the prompt response, auto-approving permission requests.
	type toolNotif struct {
		Type       string `json:"type"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Status     string `json:"status"`
		Result     string `json:"result"`
		Error      string `json:"error"`
	}

	var toolNotifs []toolNotif
	deadline := time.After(5 * time.Second)
waitForPromptResp:
	for {
		select {
		case msg := <-msgCh:
			// Auto-approve permission requests inline.
			if msg.Method == "session/request_permission" && msg.ID != nil {
				permResp := Message{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  json.RawMessage(`{"outcome":"selected"}`),
				}
				data, _ := json.Marshal(permResp)
				send(string(data))
				continue
			}
			if msg.ID != nil && string(*msg.ID) == "3" && msg.Result != nil {
				break waitForPromptResp
			}
			if msg.Method == "session/update" && msg.Params != nil {
				var notif toolNotif
				if json.Unmarshal(msg.Params, &notif) == nil && (notif.Type == "tool_call" || notif.Type == "tool_call_update") {
					toolNotifs = append(toolNotifs, notif)
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for prompt response")
		}
	}

	// Expect exactly 3 tool notifications: pending, in_progress, failed.
	if len(toolNotifs) != 3 {
		t.Fatalf("expected 3 tool notifications, got %d: %+v", len(toolNotifs), toolNotifs)
	}

	if toolNotifs[0].Type != "tool_call" || toolNotifs[0].Status != "pending" {
		t.Errorf("notification 1: expected tool_call/pending, got %s/%s", toolNotifs[0].Type, toolNotifs[0].Status)
	}
	if toolNotifs[0].ToolCallID != "tool-call-456" {
		t.Errorf("notification 1: expected toolCallId 'tool-call-456', got %q", toolNotifs[0].ToolCallID)
	}
	if toolNotifs[0].ToolName != "nonexistent_tool" {
		t.Errorf("notification 1: expected toolName 'nonexistent_tool', got %q", toolNotifs[0].ToolName)
	}

	if toolNotifs[1].Type != "tool_call_update" || toolNotifs[1].Status != "in_progress" {
		t.Errorf("notification 2: expected tool_call_update/in_progress, got %s/%s", toolNotifs[1].Type, toolNotifs[1].Status)
	}
	if toolNotifs[1].ToolCallID != "tool-call-456" {
		t.Errorf("notification 2: expected toolCallId 'tool-call-456', got %q", toolNotifs[1].ToolCallID)
	}

	if toolNotifs[2].Type != "tool_call_update" || toolNotifs[2].Status != "failed" {
		t.Errorf("notification 3: expected tool_call_update/failed, got %s/%s", toolNotifs[2].Type, toolNotifs[2].Status)
	}
	if toolNotifs[2].ToolCallID != "tool-call-456" {
		t.Errorf("notification 3: expected toolCallId 'tool-call-456', got %q", toolNotifs[2].ToolCallID)
	}
	if toolNotifs[2].Error == "" {
		t.Error("notification 3: expected non-empty error message")
	}
	if !strings.Contains(toolNotifs[2].Error, "unknown tool") {
		t.Errorf("notification 3: expected error to contain 'unknown tool', got %q", toolNotifs[2].Error)
	}

	clientWriter.Close()
	serveWg.Wait()
}

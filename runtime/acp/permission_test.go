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

func TestPermissionPlugin_Selected(t *testing.T) {
	// Test: when the client responds with outcome "selected", the tool proceeds.

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
		ID:    "tc-1",
		Name:  "greet",
		Input: json.RawMessage(`{"Name":"World"}`),
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(toolUseBlock),
			bondtest.TextEvents("done"),
		),
	}

	// Use io.Pipe for bidirectional communication.
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

	// Collect messages from server in a channel.
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

	// Run handler in goroutine.
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

	// Step 1: Initialize.
	send(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}`)
	_, ok := waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "1" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for initialize response")
	}

	// Step 2: Create session.
	send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
	_, ok = waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "2" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for session/new response")
	}
	// Drain available_commands notification.
	waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 2*time.Second)

	// Step 3: Send prompt (triggers tool call → permission request).
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"greet"}}`)

	// Step 4: Wait for the permission request from the handler.
	permMsg, ok := waitForMsg(func(m Message) bool {
		return m.Method == "session/request_permission" && m.ID != nil
	}, 5*time.Second)
	if !ok {
		t.Fatal("timed out waiting for session/request_permission")
	}

	// Verify permission request params.
	var params permissionRequestParams
	_ = json.Unmarshal(permMsg.Params, &params)
	if params.ToolCallID != "tc-1" {
		t.Errorf("expected toolCallId 'tc-1', got %q", params.ToolCallID)
	}
	if params.ToolName != "greet" {
		t.Errorf("expected toolName 'greet', got %q", params.ToolName)
	}

	// Step 5: Respond with "selected".
	permResp := Message{
		JSONRPC: "2.0",
		ID:      permMsg.ID,
		Result:  json.RawMessage(`{"outcome":"selected"}`),
	}
	data, _ := json.Marshal(permResp)
	send(string(data))

	// Step 6: Wait for the prompt response. Collect all intermediate messages.
	var collectedMsgs []Message
	var promptResp Message
	deadline := time.After(5 * time.Second)
waitLoop:
	for {
		select {
		case msg := <-msgCh:
			if msg.ID != nil && string(*msg.ID) == "3" && msg.Result != nil {
				promptResp = msg
				break waitLoop
			}
			collectedMsgs = append(collectedMsgs, msg)
		case <-deadline:
			t.Fatal("timed out waiting for prompt response")
		}
	}

	var result struct {
		StopReason string `json:"stop_reason"`
	}
	_ = json.Unmarshal(promptResp.Result, &result)
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}

	// Verify that we saw tool_call_update "completed" in collected messages.
	var foundCompleted bool
	for _, msg := range collectedMsgs {
		if msg.Method == "session/update" && msg.Params != nil {
			var notif struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			}
			if json.Unmarshal(msg.Params, &notif) == nil && notif.Type == "tool_call_update" && notif.Status == "completed" {
				foundCompleted = true
			}
		}
	}
	// Also drain any remaining messages.
drainSelected:
	for {
		select {
		case msg := <-msgCh:
			if msg.Method == "session/update" && msg.Params != nil {
				var notif struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				}
				if json.Unmarshal(msg.Params, &notif) == nil && notif.Type == "tool_call_update" && notif.Status == "completed" {
					foundCompleted = true
				}
			}
		case <-time.After(500 * time.Millisecond):
			break drainSelected
		}
	}

	if !foundCompleted {
		t.Error("expected tool_call_update with status 'completed'")
	}

	// Cleanup.
	clientWriter.Close()
	serveWg.Wait()
}

func TestPermissionPlugin_Cancelled(t *testing.T) {
	// Test: when the client responds with outcome "cancelled", the tool is skipped (ErrAbort).

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
		ID:    "tc-2",
		Name:  "greet",
		Input: json.RawMessage(`{"Name":"World"}`),
	}

	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(toolUseBlock),
			bondtest.TextEvents("done after cancel"),
		),
	}

	// Use io.Pipe for bidirectional communication.
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

	// Collect messages from server in a channel.
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

	// Run handler in goroutine.
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

	// Step 1: Initialize.
	send(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}`)
	_, ok := waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "1" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for initialize response")
	}

	// Step 2: Create session.
	send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
	_, ok = waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "2" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for session/new response")
	}
	// Drain available_commands notification.
	waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 2*time.Second)

	// Step 3: Send prompt (triggers tool call → permission request).
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"greet"}}`)

	// Drain tool_call "pending" notification that comes before the permission request.
	waitForMsg(func(m Message) bool {
		if m.Method != "session/update" || m.Params == nil {
			return false
		}
		var notif struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		}
		json.Unmarshal(m.Params, &notif) //nolint:errcheck
		return notif.Type == "tool_call" && notif.Status == "pending"
	}, 5*time.Second)

	// Step 4: Wait for the permission request from the handler.
	permMsg, ok := waitForMsg(func(m Message) bool {
		return m.Method == "session/request_permission" && m.ID != nil
	}, 5*time.Second)
	if !ok {
		t.Fatal("timed out waiting for session/request_permission")
	}

	// Step 5: Respond with "cancelled".
	permResp := Message{
		JSONRPC: "2.0",
		ID:      permMsg.ID,
		Result:  json.RawMessage(`{"outcome":"cancelled"}`),
	}
	data, _ := json.Marshal(permResp)
	send(string(data))

	// Step 6: Wait for the prompt response. Collect intermediate messages.
	var collectedMsgs []Message
	var promptResp Message
	deadline := time.After(5 * time.Second)
waitLoopCancelled:
	for {
		select {
		case msg := <-msgCh:
			if msg.ID != nil && string(*msg.ID) == "3" && msg.Result != nil {
				promptResp = msg
				break waitLoopCancelled
			}
			collectedMsgs = append(collectedMsgs, msg)
		case <-deadline:
			t.Fatal("timed out waiting for prompt response")
		}
	}

	var result struct {
		StopReason string `json:"stop_reason"`
	}
	_ = json.Unmarshal(promptResp.Result, &result)
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}

	// Verify that tool was NOT completed (ErrAbort → error result, no AfterToolCallHook).
	var foundCompleted bool
	for _, msg := range collectedMsgs {
		if msg.Method == "session/update" && msg.Params != nil {
			var notif struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			}
			if json.Unmarshal(msg.Params, &notif) == nil && notif.Type == "tool_call_update" {
				if notif.Status == "completed" {
					foundCompleted = true
				}
			}
		}
	}
	// Also drain any remaining messages.
drainCancelled:
	for {
		select {
		case msg := <-msgCh:
			if msg.Method == "session/update" && msg.Params != nil {
				var notif struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				}
				if json.Unmarshal(msg.Params, &notif) == nil && notif.Type == "tool_call_update" {
					if notif.Status == "completed" {
						foundCompleted = true
					}
				}
			}
		case <-time.After(500 * time.Millisecond):
			break drainCancelled
		}
	}

	if foundCompleted {
		t.Error("tool should NOT have completed when permission is cancelled")
	}
	// When permission is cancelled, ErrAbort is returned from BeforeToolCallHook.
	// The agent loop returns an error tool result without calling AfterToolCallHook,
	// so no "failed" notification is sent by the toolNotifier. The key assertion is
	// that the tool did NOT complete successfully.

	// Cleanup.
	clientWriter.Close()
	serveWg.Wait()
}

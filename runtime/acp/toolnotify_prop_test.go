package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

// TestProperty_ToolLifecycleNotifications verifies that for any tool call with
// varying outcomes (success/failure), the handler emits exactly 3 notifications
// in the correct sequence: tool_call (pending) → tool_call_update (in_progress)
// → tool_call_update (completed/failed), all referencing the same toolCallId.
//
// **Validates: Requirements 5.1, 5.2, 5.3, 5.4**
func TestProperty_ToolLifecycleNotifications(t *testing.T) {
	f := func(scenario toolCallScenario) bool {
		toolName := scenario.ToolName
		toolCallID := scenario.ToolCallID
		shouldFail := scenario.ShouldFail

		// Create a tool that succeeds or fails based on the scenario.
		var tool bond.Tool
		if shouldFail {
			var err error
			tool, err = bond.NewFuncTool(
				func(ctx context.Context, input struct{}) (string, error) {
					return "", fmt.Errorf("tool execution failed")
				},
				bond.FuncToolOptions{
					Name:        toolName,
					Description: "A test tool that fails",
				},
			)
			if err != nil {
				t.Logf("failed to create failing tool: %v", err)
				return false
			}
		} else {
			var err error
			tool, err = bond.NewFuncTool(
				func(ctx context.Context, input struct{}) (string, error) {
					return "tool result ok", nil
				},
				bond.FuncToolOptions{
					Name:        toolName,
					Description: "A test tool that succeeds",
				},
			)
			if err != nil {
				t.Logf("failed to create succeeding tool: %v", err)
				return false
			}
		}

		toolUseBlock := &bond.ToolUseBlock{
			ID:    toolCallID,
			Name:  toolName,
			Input: json.RawMessage(`{}`),
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
				Tools: []bond.Tool{tool},
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

		// Initialize.
		send(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}`)
		_, ok := waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "1" }, 3*time.Second)
		if !ok {
			t.Logf("timed out waiting for initialize response")
			cancel()
			clientWriter.Close()
			serveWg.Wait()
			return false
		}

		// Create session.
		send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
		waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "2" }, 3*time.Second)
		waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 3*time.Second)

		// Send prompt.
		send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"run tool"}}`)

		// Collect all messages until the prompt response, auto-approving permission requests.
		type toolNotifT struct {
			Type       string `json:"type"`
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
			Status     string `json:"status"`
			Result     string `json:"result"`
			Error      string `json:"error"`
		}

		var toolNotifs []toolNotifT
		deadline := time.After(8 * time.Second)
	loop:
		for {
			select {
			case msg := <-msgCh:
				// Auto-approve permission requests.
				if msg.Method == "session/request_permission" && msg.ID != nil {
					resp := Message{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  json.RawMessage(`{"outcome":"selected"}`),
					}
					data, _ := json.Marshal(resp)
					send(string(data))
					continue
				}
				if msg.ID != nil && string(*msg.ID) == "3" && msg.Result != nil {
					break loop
				}
				if msg.Method == "session/update" && msg.Params != nil {
					var notif toolNotifT
					if json.Unmarshal(msg.Params, &notif) == nil && (notif.Type == "tool_call" || notif.Type == "tool_call_update") {
						toolNotifs = append(toolNotifs, notif)
					}
				}
			case <-deadline:
				t.Logf("timed out waiting for prompt response (toolName=%q, shouldFail=%v)", toolName, shouldFail)
				cancel()
				clientWriter.Close()
				serveWg.Wait()
				return false
			}
		}

		// Cleanup.
		clientWriter.Close()
		serveWg.Wait()

		// Verify: exactly 3 notifications per tool call.
		if len(toolNotifs) != 3 {
			t.Logf("expected 3 tool notifications, got %d (toolName=%q, shouldFail=%v): %+v",
				len(toolNotifs), toolName, shouldFail, toolNotifs)
			return false
		}

		if toolNotifs[0].Type != "tool_call" || toolNotifs[0].Status != "pending" {
			t.Logf("notification 1: expected tool_call/pending, got %s/%s", toolNotifs[0].Type, toolNotifs[0].Status)
			return false
		}
		if toolNotifs[0].ToolCallID != toolCallID {
			t.Logf("notification 1: expected toolCallId %q, got %q", toolCallID, toolNotifs[0].ToolCallID)
			return false
		}
		if toolNotifs[0].ToolName != toolName {
			t.Logf("notification 1: expected toolName %q, got %q", toolName, toolNotifs[0].ToolName)
			return false
		}

		if toolNotifs[1].Type != "tool_call_update" || toolNotifs[1].Status != "in_progress" {
			t.Logf("notification 2: expected tool_call_update/in_progress, got %s/%s", toolNotifs[1].Type, toolNotifs[1].Status)
			return false
		}
		if toolNotifs[1].ToolCallID != toolCallID {
			t.Logf("notification 2: expected toolCallId %q, got %q", toolCallID, toolNotifs[1].ToolCallID)
			return false
		}

		if toolNotifs[2].Type != "tool_call_update" {
			t.Logf("notification 3: expected type 'tool_call_update', got %q", toolNotifs[2].Type)
			return false
		}
		if toolNotifs[2].ToolCallID != toolCallID {
			t.Logf("notification 3: expected toolCallId %q, got %q", toolCallID, toolNotifs[2].ToolCallID)
			return false
		}

		if shouldFail {
			if toolNotifs[2].Status != "failed" {
				t.Logf("notification 3: expected status 'failed', got %q", toolNotifs[2].Status)
				return false
			}
			if toolNotifs[2].Error == "" {
				t.Logf("notification 3: expected non-empty error")
				return false
			}
		} else {
			if toolNotifs[2].Status != "completed" {
				t.Logf("notification 3: expected status 'completed', got %q", toolNotifs[2].Status)
				return false
			}
			if toolNotifs[2].Result == "" {
				t.Logf("notification 3: expected non-empty result")
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 11: Tool Lifecycle Notifications failed: %v", err)
	}
}

// toolCallScenario is a generated test scenario for a tool call.
type toolCallScenario struct {
	ToolName   string
	ToolCallID string
	ShouldFail bool
}

func (toolCallScenario) Generate(rng *rand.Rand, size int) reflect.Value {
	return reflect.ValueOf(toolCallScenario{
		ToolName:   generateToolName(rng),
		ToolCallID: generateToolCallID(rng),
		ShouldFail: rng.Intn(2) == 0,
	})
}

func generateToolName(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz_"
	length := 3 + rng.Intn(13)
	b := make([]byte, length)
	b[0] = "abcdefghijklmnopqrstuvwxyz"[rng.Intn(26)]
	for i := 1; i < length; i++ {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

func generateToolCallID(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	seg1 := make([]byte, 4)
	seg2 := make([]byte, 4)
	for i := range seg1 {
		seg1[i] = chars[rng.Intn(len(chars))]
		seg2[i] = chars[rng.Intn(len(chars))]
	}
	return "toolcall-" + string(seg1) + "-" + string(seg2)
}

package acp

import (
	"context"
	"encoding/json"
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

// TestProperty_PermissionRequestBeforeExecution verifies that for any tool call
// about to be executed, the handler sends a `session/request_permission` request
// to the client BEFORE any `tool_call_update` with status "in_progress" or
// "completed" appears in the message stream. This confirms that permission is
// always requested before execution begins.
//
// **Validates: Requirements 7.1**
func TestProperty_PermissionRequestBeforeExecution(t *testing.T) {
	f := func(scenario permissionScenario) bool {
		toolName := scenario.ToolName
		toolCallID := scenario.ToolCallID

		// Create a simple tool that always succeeds.
		tool, err := bond.NewFuncTool(
			func(ctx context.Context, input struct{}) (string, error) {
				return "executed", nil
			},
			bond.FuncToolOptions{
				Name:        toolName,
				Description: "A test tool",
			},
		)
		if err != nil {
			t.Logf("failed to create tool: %v", err)
			return false
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
				n, rerr := clientReader.Read(buf)
				if rerr != nil {
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
		// Drain available_commands notification.
		waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 3*time.Second)

		// Send prompt (triggers tool call → permission request).
		send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"run"}}`)

		// Collect all messages from the server until the prompt response.
		// When we see the permission request, send back "selected" to allow execution.
		type orderedMsg struct {
			Method string
			Type   string // session/update type
			Status string // tool_call_update status
			IsReq  bool   // is a request (has id and method)
		}

		var orderedMsgs []orderedMsg
		deadline := time.After(8 * time.Second)
	loop:
		for {
			select {
			case msg := <-msgCh:
				// If this is the permission request, record it and respond.
				if msg.Method == "session/request_permission" && msg.ID != nil {
					orderedMsgs = append(orderedMsgs, orderedMsg{
						Method: "session/request_permission",
						IsReq:  true,
					})
					// Send "selected" response to allow execution.
					resp := Message{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  json.RawMessage(`{"outcome":"selected"}`),
					}
					data, _ := json.Marshal(resp)
					send(string(data))
					continue
				}
				// If this is the prompt response, we're done.
				if msg.ID != nil && string(*msg.ID) == "3" && msg.Result != nil {
					break loop
				}
				// If this is a session/update notification, record relevant details.
				if msg.Method == "session/update" && msg.Params != nil {
					var notif struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					}
					if json.Unmarshal(msg.Params, &notif) == nil {
						if notif.Type == "tool_call" || notif.Type == "tool_call_update" {
							orderedMsgs = append(orderedMsgs, orderedMsg{
								Method: "session/update",
								Type:   notif.Type,
								Status: notif.Status,
							})
						}
					}
				}
			case <-deadline:
				t.Logf("timed out waiting for prompt response (toolName=%q, toolCallID=%q)", toolName, toolCallID)
				cancel()
				clientWriter.Close()
				serveWg.Wait()
				return false
			}
		}

		// Cleanup.
		clientWriter.Close()
		serveWg.Wait()

		// THE KEY PROPERTY: session/request_permission must appear BEFORE any
		// tool_call_update with status "in_progress" or "completed".
		permissionIdx := -1
		for i, m := range orderedMsgs {
			if m.Method == "session/request_permission" {
				permissionIdx = i
				break
			}
		}

		if permissionIdx == -1 {
			t.Logf("no session/request_permission found in messages (toolName=%q, toolCallID=%q)", toolName, toolCallID)
			return false
		}

		// Verify no execution-related notifications appear before the permission request.
		for i := 0; i < permissionIdx; i++ {
			m := orderedMsgs[i]
			if m.Type == "tool_call_update" && (m.Status == "in_progress" || m.Status == "completed") {
				t.Logf("tool_call_update with status %q appeared at index %d BEFORE permission request at index %d",
					m.Status, i, permissionIdx)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 50}); err != nil {
		t.Errorf("Feature: acp-handler, Property 12: Permission Request Before Execution failed: %v", err)
	}
}

// permissionScenario generates random tool names and tool IDs for testing
// the permission request ordering property.
type permissionScenario struct {
	ToolName   string
	ToolCallID string
}

func (permissionScenario) Generate(rng *rand.Rand, size int) reflect.Value {
	return reflect.ValueOf(permissionScenario{
		ToolName:   generatePermToolName(rng),
		ToolCallID: generatePermToolCallID(rng),
	})
}

func generatePermToolName(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz_"
	length := 3 + rng.Intn(13)
	b := make([]byte, length)
	b[0] = "abcdefghijklmnopqrstuvwxyz"[rng.Intn(26)]
	for i := 1; i < length; i++ {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

func generatePermToolCallID(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	seg1 := make([]byte, 4)
	seg2 := make([]byte, 4)
	for i := range seg1 {
		seg1[i] = chars[rng.Intn(len(chars))]
		seg2[i] = chars[rng.Intn(len(chars))]
	}
	return "tc-" + string(seg1) + "-" + string(seg2)
}

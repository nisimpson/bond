package acpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
)

// TODO: implement ReadWriter
type MockTransport struct{}

// TestProperty_TextDeltaForwarding verifies that sequences of agent_message_chunk
// notifications sent during a prompt are translated into exactly N StreamEventTextDelta
// events with matching delta text and preserved ordering.
//
// Feature: acp-proxy, Property 4: Text Delta Forwarding
// **Validates: Requirements 4.2**
func TestProperty_TextDeltaForwarding(t *testing.T) {
	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate 1..5 random delta strings.
		numDeltas := 1 + rnd.Intn(5)
		deltas := make([]string, numDeltas)
		for i := range deltas {
			deltas[i] = "delta-" + generateAlphanumeric(rnd, 4+rnd.Intn(12))
		}

		// Set up pipes for mock server.
		clientToServerR, clientToServerW := io.Pipe()
		serverToClientR, serverToClientW := io.Pipe()

		transport := newPipeReadWriter(serverToClientR, clientToServerW)
		opts := ClientOptions{WorkingDir: "/tmp"}
		client := NewClient(transport, opts)

		// Mock server goroutine.
		go func() {
			defer serverToClientW.Close()
			scanner := bufio.NewScanner(clientToServerR)
			for scanner.Scan() {
				var msg Message
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					continue
				}

				switch msg.Method {
				case "initialize":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(initializeResponse{
						ProtocolVersion:   1,
						AgentCapabilities: Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
						AgentInfo:         AgentInfo{Name: "mock", Version: "1.0"},
					})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/new":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionNewResponse{SessionID: "test-session"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/prompt":
					// Send N agent_message_chunk notifications before the response.
					for _, delta := range deltas {
						update, _ := json.Marshal(agentMessageChunk{
							SessionUpdate: "agent_message_chunk",
							MessageID:     "msg-1",
							Content:       &contentBlock{Type: "text", Text: delta},
						})
						envelope, _ := json.Marshal(sessionUpdateEnvelope{
							SessionID: "test-session",
							Update:    update,
						})
						notif := Message{
							JSONRPC: "2.0",
							Method:  "session/update",
							Params:  envelope,
						}
						data, _ := json.Marshal(notif)
						_, _ = serverToClientW.Write(append(data, '\n'))
					}

					// Send the prompt response.
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))
				}
			}
		}()

		// Start the client.
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			t.Logf("Start failed: %v", err)
			clientToServerR.Close()
			return false
		}

		// Call Stream with a user message.
		messages := []bond.Message{
			{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
		}

		var textDeltas []string
		agent := client.Agent()
		for event, err := range agent.Stream(ctx, messages) {
			if err != nil {
				t.Logf("Stream error: %v", err)
				client.Close()
				clientToServerR.Close()
				return false
			}
			if event.Type == bond.StreamEventTextDelta {
				textDeltas = append(textDeltas, event.TextDelta)
			}
		}

		client.Close()
		clientToServerR.Close()

		// Verify exactly N text deltas with matching content in order.
		if len(textDeltas) != numDeltas {
			t.Logf("expected %d text deltas, got %d", numDeltas, len(textDeltas))
			return false
		}
		for i, got := range textDeltas {
			if got != deltas[i] {
				t.Logf("delta[%d]: got %q, want %q", i, got, deltas[i])
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 30}); err != nil {
		t.Errorf("Text delta forwarding property failed: %v", err)
	}
}

// TestProperty_ToolNotificationForwarding verifies that tool_call and tool_call_update
// notifications are translated into StreamEventToolUse events with correct fields.
//
// Feature: acp-proxy, Property 5: Tool Notification Forwarding
// **Validates: Requirements 4.7, 4.8**
func TestProperty_ToolNotificationForwarding(t *testing.T) {
	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate a mix of tool_call and tool_call_update notifications.
		type toolNotif struct {
			notifType  string // "tool_call" or "tool_call_update"
			toolCallID string
			toolName   string
			status     string
		}

		numNotifs := 1 + rnd.Intn(4)
		notifs := make([]toolNotif, numNotifs)
		statuses := []string{"pending", "in_progress", "completed", "failed"}
		for i := range notifs {
			if rnd.Intn(2) == 0 {
				notifs[i] = toolNotif{
					notifType:  "tool_call",
					toolCallID: "tc-" + generateAlphanumeric(rnd, 6),
					toolName:   "tool_" + generateAlphanumeric(rnd, 4),
					status:     "pending",
				}
			} else {
				notifs[i] = toolNotif{
					notifType:  "tool_call_update",
					toolCallID: "tc-" + generateAlphanumeric(rnd, 6),
					toolName:   "tool_" + generateAlphanumeric(rnd, 4),
					status:     statuses[rnd.Intn(len(statuses))],
				}
			}
		}

		// Set up pipes for mock server.
		clientToServerR, clientToServerW := io.Pipe()
		serverToClientR, serverToClientW := io.Pipe()

		transport := newPipeReadWriter(serverToClientR, clientToServerW)
		opts := ClientOptions{WorkingDir: "/tmp"}
		client := NewClient(transport, opts)

		// Mock server goroutine.
		go func() {
			defer serverToClientW.Close()
			scanner := bufio.NewScanner(clientToServerR)
			for scanner.Scan() {
				var msg Message
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					continue
				}

				switch msg.Method {
				case "initialize":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(initializeResponse{
						ProtocolVersion:   1,
						AgentCapabilities: Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
						AgentInfo:         AgentInfo{Name: "mock", Version: "1.0"},
					})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/new":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionNewResponse{SessionID: "test-session"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/prompt":
					// Send tool notifications before the response.
					for _, tn := range notifs {
						var notifMsg Message
						notifMsg.JSONRPC = "2.0"
						notifMsg.Method = "session/update"

						if tn.notifType == "tool_call" {
							update, _ := json.Marshal(toolCallNotification{
								SessionUpdate: "tool_call",
								ToolCallID:    tn.toolCallID,
								Title:         tn.toolName,
								Status:        tn.status,
							})
							envelope, _ := json.Marshal(sessionUpdateEnvelope{
								SessionID: "test-session",
								Update:    update,
							})
							notifMsg.Params = envelope
						} else {
							update, _ := json.Marshal(toolCallUpdateNotification{
								SessionUpdate: "tool_call_update",
								ToolCallID:    tn.toolCallID,
								Status:        tn.status,
							})
							envelope, _ := json.Marshal(sessionUpdateEnvelope{
								SessionID: "test-session",
								Update:    update,
							})
							notifMsg.Params = envelope
						}

						data, _ := json.Marshal(notifMsg)
						_, _ = serverToClientW.Write(append(data, '\n'))
					}

					// Send the prompt response.
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))
				}
			}
		}()

		// Start the client.
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			t.Logf("Start failed: %v", err)
			clientToServerR.Close()
			return false
		}

		// Call Stream with a user message.
		messages := []bond.Message{
			{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "do stuff"}}},
		}

		type toolEvent struct {
			toolCallID string
			toolName   string
			status     string
		}
		var toolEvents []toolEvent

		agent := client.Agent()
		for event, err := range agent.Stream(ctx, messages) {
			if err != nil {
				t.Logf("Stream error: %v", err)
				client.Close()
				clientToServerR.Close()
				return false
			}
			if event.Type == bond.StreamEventToolUse && event.ToolUse != nil {
				status, _ := event.Metadata["status"].(string)
				toolEvents = append(toolEvents, toolEvent{
					toolCallID: event.ToolUse.ID,
					toolName:   event.ToolUse.Name,
					status:     status,
				})
			}
		}

		client.Close()
		clientToServerR.Close()

		// Verify correct number and content of tool events.
		if len(toolEvents) != numNotifs {
			t.Logf("expected %d tool events, got %d", numNotifs, len(toolEvents))
			return false
		}
		for i, got := range toolEvents {
			if got.toolCallID != notifs[i].toolCallID {
				t.Logf("event[%d] toolCallID: got %q, want %q", i, got.toolCallID, notifs[i].toolCallID)
				return false
			}
			// tool_call carries the title as the tool name; tool_call_update does not.
			expectedName := ""
			if notifs[i].notifType == "tool_call" {
				expectedName = notifs[i].toolName
			}
			if got.toolName != expectedName {
				t.Logf("event[%d] toolName: got %q, want %q", i, got.toolName, expectedName)
				return false
			}
			if got.status != notifs[i].status {
				t.Logf("event[%d] status: got %q, want %q", i, got.status, notifs[i].status)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 30}); err != nil {
		t.Errorf("Tool notification forwarding property failed: %v", err)
	}
}

// TestProperty_LastUserMessageExtraction verifies that ProxyAgent extracts
// the text from the last user message's TextBlock and sends it as the prompt.
//
// Feature: acp-proxy, Property 7: Last User Message Extraction
// **Validates: Requirements 4.1, 7.2**
func TestProperty_LastUserMessageExtraction(t *testing.T) {
	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate a slice of messages where the last one is a user message with TextBlock.
		numPrecedingMessages := rnd.Intn(4) // 0-3 preceding messages
		expectedText := "user-prompt-" + generateAlphanumeric(rnd, 8+rnd.Intn(20))

		messages := make([]bond.Message, 0, numPrecedingMessages+1)
		for i := 0; i < numPrecedingMessages; i++ {
			if rnd.Intn(2) == 0 {
				messages = append(messages, bond.Message{
					Role:    bond.RoleAssistant,
					Content: []bond.Block{&bond.TextBlock{Text: "assistant-" + generateAlphanumeric(rnd, 6)}},
				})
			} else {
				messages = append(messages, bond.Message{
					Role:    bond.RoleUser,
					Content: []bond.Block{&bond.TextBlock{Text: "older-user-" + generateAlphanumeric(rnd, 6)}},
				})
			}
		}
		// Append the final user message.
		messages = append(messages, bond.Message{
			Role:    bond.RoleUser,
			Content: []bond.Block{&bond.TextBlock{Text: expectedText}},
		})

		// Set up pipes for mock server.
		clientToServerR, clientToServerW := io.Pipe()
		serverToClientR, serverToClientW := io.Pipe()

		transport := newPipeReadWriter(serverToClientR, clientToServerW)
		opts := ClientOptions{WorkingDir: "/tmp"}
		client := NewClient(transport, opts)

		// Track the prompt text received by the mock server.
		var capturedPrompt string
		var mu sync.Mutex

		// Mock server goroutine.
		go func() {
			defer serverToClientW.Close()
			scanner := bufio.NewScanner(clientToServerR)
			for scanner.Scan() {
				var msg Message
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					continue
				}

				switch msg.Method {
				case "initialize":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(initializeResponse{
						ProtocolVersion:   1,
						AgentCapabilities: Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
						AgentInfo:         AgentInfo{Name: "mock", Version: "1.0"},
					})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/new":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionNewResponse{SessionID: "test-session"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/prompt":
					var params sessionPromptRequest
					json.Unmarshal(msg.Params, &params) //nolint:errcheck
					mu.Lock()
					capturedPrompt = params.Prompt[0].Text
					mu.Unlock()

					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))
				}
			}
		}()

		// Start the client.
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			t.Logf("Start failed: %v", err)
			clientToServerR.Close()
			return false
		}

		// Call Stream with the generated messages.
		agent := client.Agent()
		for _, err := range agent.Stream(ctx, messages) {
			if err != nil {
				t.Logf("Stream error: %v", err)
				client.Close()
				clientToServerR.Close()
				return false
			}
		}

		client.Close()
		clientToServerR.Close()

		// Verify the captured prompt matches the expected text.
		mu.Lock()
		defer mu.Unlock()
		if capturedPrompt != expectedText {
			t.Logf("prompt mismatch: got %q, want %q", capturedPrompt, expectedText)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 30}); err != nil {
		t.Errorf("Last user message extraction property failed: %v", err)
	}
}

// TestProperty_SequentialPromptAccumulation verifies that multiple sequential
// calls to ProxyAgent.Stream within the same session each send a session/prompt
// request and yield events independently.
//
// Feature: acp-proxy, Property 10: Sequential Prompt Accumulation
// **Validates: Requirements 7.3**
func TestProperty_SequentialPromptAccumulation(t *testing.T) {
	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate 2..4 prompts.
		numPrompts := 2 + rnd.Intn(3)
		prompts := make([]string, numPrompts)
		for i := range prompts {
			prompts[i] = fmt.Sprintf("prompt-%d-%s", i, generateAlphanumeric(rnd, 6))
		}

		// Set up pipes for mock server.
		clientToServerR, clientToServerW := io.Pipe()
		serverToClientR, serverToClientW := io.Pipe()

		transport := newPipeReadWriter(serverToClientR, clientToServerW)
		opts := ClientOptions{WorkingDir: "/tmp"}
		client := NewClient(transport, opts)

		// Track all prompt messages received by mock server.
		var receivedPrompts []string
		var mu sync.Mutex

		// Mock server goroutine.
		go func() {
			defer serverToClientW.Close()
			scanner := bufio.NewScanner(clientToServerR)
			for scanner.Scan() {
				var msg Message
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					continue
				}

				switch msg.Method {
				case "initialize":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(initializeResponse{
						ProtocolVersion:   1,
						AgentCapabilities: Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
						AgentInfo:         AgentInfo{Name: "mock", Version: "1.0"},
					})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/new":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionNewResponse{SessionID: "test-session"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))

				case "session/prompt":
					var params sessionPromptRequest
					json.Unmarshal(msg.Params, &params) //nolint:errcheck
					mu.Lock()
					receivedPrompts = append(receivedPrompts, params.Prompt[0].Text)
					mu.Unlock()

					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))
				}
			}
		}()

		// Start the client.
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			t.Logf("Start failed: %v", err)
			clientToServerR.Close()
			return false
		}

		// Call Stream N times with different messages.
		agent := client.Agent()
		for i, prompt := range prompts {
			messages := []bond.Message{
				{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: prompt}}},
			}

			var gotStop bool
			for event, err := range agent.Stream(ctx, messages) {
				if err != nil {
					t.Logf("Stream[%d] error: %v", i, err)
					client.Close()
					clientToServerR.Close()
					return false
				}
				if event.Type == bond.StreamEventStop {
					gotStop = true
				}
			}
			if !gotStop {
				t.Logf("Stream[%d] did not receive stop event", i)
				client.Close()
				clientToServerR.Close()
				return false
			}
		}

		client.Close()
		clientToServerR.Close()

		// Verify N prompts were received in order.
		mu.Lock()
		defer mu.Unlock()
		if len(receivedPrompts) != numPrompts {
			t.Logf("expected %d prompts, got %d", numPrompts, len(receivedPrompts))
			return false
		}
		for i, got := range receivedPrompts {
			if got != prompts[i] {
				t.Logf("prompt[%d]: got %q, want %q", i, got, prompts[i])
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 20,
		Values: func(values []reflect.Value, rnd *rand.Rand) {
			values[0] = reflect.ValueOf(rnd.Int63())
		},
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Errorf("Sequential prompt accumulation property failed: %v", err)
	}
}

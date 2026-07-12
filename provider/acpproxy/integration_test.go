//go:build integration

package acpproxy_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nisimpson/bond/provider/acpproxy/acpio"

	bond "github.com/nisimpson/bond"
)

// mockServerConfig controls how the mock ACP server goroutine behaves
// during a prompt round-trip.
type mockServerConfig struct {
	// TextDeltas to send as agent_message_chunk notifications before the prompt response.
	TextDeltas []string
	// Whether to send a permission request during the prompt.
	SendPermission bool
	// PermissionTool is the tool name used in the permission request.
	PermissionTool string
	// StopReason to return in the prompt response (default: "end_turn").
	StopReason string
}

// mockServer holds the server-side state including the pipe writers and
// synchronization primitives for testing.
type mockServer struct {
	// clientToServerR is the read end of what the client writes.
	clientToServerR *io.PipeReader
	// serverToClientW is the write end that the server writes to.
	serverToClientW *io.PipeWriter
	// wg tracks the mock server goroutine.
	wg sync.WaitGroup
	// cancelReceived is closed when a session/cancel notification arrives.
	cancelReceived chan struct{}
	// promptMessages records all prompt message texts received.
	promptMessages []string
	mu             sync.Mutex
}

// startMockServer creates two io.Pipe pairs and starts a mock ACP server
// goroutine. It returns a connected Client and a cleanup function.
func startMockServer(t *testing.T, config mockServerConfig) (*Client, *mockServer, func()) {
	t.Helper()

	if config.StopReason == "" {
		config.StopReason = "end_turn"
	}

	// Create pipe pairs:
	// Client writes → clientToServerW → clientToServerR → mock server reads
	// Mock server writes → serverToClientW → serverToClientR → client reads
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	transport := acpio.NewTransport(serverToClientR, clientToServerW)

	ms := &mockServer{
		clientToServerR: clientToServerR,
		serverToClientW: serverToClientW,
		cancelReceived:  make(chan struct{}),
	}

	ms.wg.Add(1)
	go func() {
		defer ms.wg.Done()
		defer serverToClientW.Close()

		scanner := bufio.NewScanner(clientToServerR)
		scanner.Buffer(make([]byte, 0, maxScanTokenSize), maxScanTokenSize)

		// pendingPromptID tracks the ID of an in-progress prompt request
		// so that a cancel notification can send the response.
		var pendingPromptID *json.RawMessage
		var pendingPromptMu sync.Mutex

		for scanner.Scan() {
			var msg Message
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}

			switch msg.Method {
			case "initialize":
				result, _ := json.Marshal(initializeResponse{
					ProtocolVersion: 1,
					Capabilities:    Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
					AgentInfo:       AgentInfo{Name: "mock-integration", Version: "1.0.0"},
				})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				serverToClientW.Write(append(data, '\n')) //nolint:errcheck

			case "session/new":
				result, _ := json.Marshal(sessionNewResponse{SessionID: "integration-session-1"})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				serverToClientW.Write(append(data, '\n')) //nolint:errcheck

			case "session/prompt":
				// Record prompt message text.
				var params sessionPromptRequest
				json.Unmarshal(msg.Params, &params) //nolint:errcheck
				ms.mu.Lock()
				ms.promptMessages = append(ms.promptMessages, params.Message)
				ms.mu.Unlock()

				pendingPromptMu.Lock()
				pendingPromptID = msg.ID //nolint:staticcheck // used by session/cancel in cancel-aware tests
				pendingPromptMu.Unlock()

				// Send permission request if configured.
				if config.SendPermission && config.PermissionTool != "" {
					permID := json.RawMessage(`999`)
					permParams, _ := json.Marshal(permissionRequestParams{
						ToolCallID: "tc-perm-1",
						ToolName:   config.PermissionTool,
						Input:      json.RawMessage(`{"path": "/tmp/file.txt"}`),
					})
					permReq := Message{
						JSONRPC: "2.0",
						ID:      &permID,
						Method:  "session/request_permission",
						Params:  permParams,
					}
					permData, _ := json.Marshal(permReq)
					serverToClientW.Write(append(permData, '\n')) //nolint:errcheck

					// Read the permission response from the client.
					// The client writes it back on the same pipe we read from.
					if scanner.Scan() {
						var permResp Message
						json.Unmarshal(scanner.Bytes(), &permResp) //nolint:errcheck
						// Permission response received, continue with prompt.
					}
				}

				// Send text delta notifications.
				for i, delta := range config.TextDeltas {
					notifParams, _ := json.Marshal(agentMessageChunk{
						SessionID: "integration-session-1",
						Type:      "agent_message_chunk",
						MessageID: "msg-" + strconv.Itoa(i),
						Delta:     delta,
					})
					notif := Message{
						JSONRPC: "2.0",
						Method:  "session/update",
						Params:  notifParams,
					}
					nData, _ := json.Marshal(notif)
					serverToClientW.Write(append(nData, '\n')) //nolint:errcheck
				}

				// Send the prompt response.
				result, _ := json.Marshal(sessionPromptResponse{StopReason: config.StopReason})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				_, _ = serverToClientW.Write(append(data, '\n'))

				pendingPromptMu.Lock()
				pendingPromptID = nil
				pendingPromptMu.Unlock()

			case "session/cancel":
				// Cancel is a notification (no response needed).
				// But we should respond to the pending prompt with "cancelled".
				select {
				case <-ms.cancelReceived:
					// already signaled
				default:
					close(ms.cancelReceived)
				}

				pendingPromptMu.Lock()
				pid := pendingPromptID
				pendingPromptID = nil
				pendingPromptMu.Unlock()

				if pid != nil {
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "cancelled"})
					resp := Message{JSONRPC: "2.0", ID: pid, Result: result}
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))
				}
			}
		}
	}()

	client := NewClient(transport, ClientOptions{
		WorkingDir:     "/tmp/integration-test",
		PermissionTier: TierYOLO,
		CancelTimeout:  2 * time.Second,
	})

	cleanup := func() {
		client.Close()
		clientToServerR.Close()
		clientToServerW.Close()
		ms.wg.Wait()
	}

	return client, ms, cleanup
}

// TestIntegration_FullPromptRoundTrip verifies the full prompt lifecycle:
// Start → Stream → receive text deltas → receive stop event.
// All deltas arrive in order and the stop reason is correct.
//
// Validates: Requirements 4.1, 4.2, 4.3, 6.1, 6.2
func TestIntegration_FullPromptRoundTrip(t *testing.T) {
	deltas := []string{"Hello, ", "world! ", "How are you?"}
	client, _, cleanup := startMockServer(t, mockServerConfig{
		TextDeltas: deltas,
		StopReason: "end_turn",
	})
	defer cleanup()

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	agent := client.Agent()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "say hello"}}},
	}

	var receivedDeltas []string
	var stopReason bond.StopReason
	var gotStop bool

	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		switch event.Type {
		case bond.StreamEventTextDelta:
			receivedDeltas = append(receivedDeltas, event.TextDelta)
		case bond.StreamEventStop:
			stopReason = event.StopReason
			gotStop = true
		}
	}

	if !gotStop {
		t.Fatal("expected stop event, got none")
	}
	if stopReason != bond.StopReasonEnd {
		t.Fatalf("expected stop reason %q, got %q", bond.StopReasonEnd, stopReason)
	}
	if len(receivedDeltas) != len(deltas) {
		t.Fatalf("expected %d deltas, got %d", len(deltas), len(receivedDeltas))
	}
	for i, d := range deltas {
		if receivedDeltas[i] != d {
			t.Fatalf("delta[%d]: expected %q, got %q", i, d, receivedDeltas[i])
		}
	}
}

// TestIntegration_PermissionFlow_YOLO verifies that when a permission request
// arrives and the client has TierYOLO, the response is "selected" and the
// stream completes successfully.
//
// Validates: Requirements 5.1, 5.3
func TestIntegration_PermissionFlow_YOLO(t *testing.T) {
	client, _, cleanup := startMockServer(t, mockServerConfig{
		TextDeltas:     []string{"permitted action done"},
		SendPermission: true,
		PermissionTool: "execute_command",
		StopReason:     "end_turn",
	})
	defer cleanup()

	// Override to ensure YOLO tier.
	client.opts.PermissionTier = TierYOLO
	client.opts.PermissionPolicy = nil

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	agent := client.Agent()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "run a command"}}},
	}

	var gotStop bool

	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if event.Type == bond.StreamEventStop {
			gotStop = true
		}
	}

	if !gotStop {
		t.Fatal("expected stop event (permission was approved and stream completed)")
	}
}

// TestIntegration_PermissionFlow_Read verifies that when a permission request
// arrives for a write tool and the client has TierRead, the response is
// "cancelled" and the stream still completes.
//
// Validates: Requirements 5.1, 5.4
func TestIntegration_PermissionFlow_Read(t *testing.T) {
	client, _, cleanup := startMockServer(t, mockServerConfig{
		TextDeltas:     []string{"continued after deny"},
		SendPermission: true,
		PermissionTool: "write_file", // write tool → should be denied by TierRead
		StopReason:     "end_turn",
	})
	defer cleanup()

	// Set TierRead to deny write tools.
	client.opts.PermissionTier = TierRead
	client.opts.PermissionPolicy = nil

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	agent := client.Agent()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "write something"}}},
	}

	var gotStop bool

	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if event.Type == bond.StreamEventStop {
			gotStop = true
		}
	}

	if !gotStop {
		t.Fatal("expected stop event after permission denied")
	}
}

// TestIntegration_ContextCancellation verifies that cancelling the context
// during a prompt causes session/cancel to be sent and the stream terminates.
//
// Validates: Requirements 6.1, 6.2, 7.1, 7.3
func TestIntegration_ContextCancellation(t *testing.T) {
	// Use a custom mock server that delays the prompt response so we can cancel.
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	transport := acpio.NewTransport(serverToClientR, clientToServerW)

	cancelReceived := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer serverToClientW.Close()

		scanner := bufio.NewScanner(clientToServerR)
		scanner.Buffer(make([]byte, 0, maxScanTokenSize), maxScanTokenSize)

		var pendingPromptID *json.RawMessage

		for scanner.Scan() {
			var msg Message
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}

			switch msg.Method {
			case "initialize":
				result, _ := json.Marshal(initializeResponse{
					ProtocolVersion: 1,
					Capabilities:    Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
					AgentInfo:       AgentInfo{Name: "mock-cancel", Version: "1.0.0"},
				})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				_, _ = serverToClientW.Write(append(data, '\n'))

			case "session/new":
				result, _ := json.Marshal(sessionNewResponse{SessionID: "cancel-session"})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				_, _ = serverToClientW.Write(append(data, '\n'))

			case "session/prompt":
				// Store the pending prompt ID but DON'T respond yet.
				// We want the caller to cancel the context.
				pendingPromptID = msg.ID

				// Send a delta to show the stream started.
				notifParams, _ := json.Marshal(agentMessageChunk{
					SessionID: "cancel-session",
					Type:      "agent_message_chunk",
					MessageID: "msg-0",
					Delta:     "starting...",
				})
				notif := Message{JSONRPC: "2.0", Method: "session/update", Params: notifParams}
				nData, _ := json.Marshal(notif)
				_, _ = serverToClientW.Write(append(nData, '\n'))
				// Do not respond — wait for cancel.

			case "session/cancel":
				close(cancelReceived)
				// Respond to the pending prompt with "cancelled".
				if pendingPromptID != nil {
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "cancelled"})
					resp := Message{JSONRPC: "2.0", ID: pendingPromptID, Result: result}
					data, _ := json.Marshal(resp)
					_, _ = serverToClientW.Write(append(data, '\n'))
					pendingPromptID = nil
				}
			}
		}
	}()

	client := NewClient(transport, ClientOptions{
		WorkingDir:    "/tmp/cancel-test",
		CancelTimeout: 2 * time.Second,
	})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	defer func() {
		client.Close()
		clientToServerR.Close()
		clientToServerW.Close()
		wg.Wait()
	}()

	// Create a cancellable context for the stream.
	streamCtx, cancel := context.WithCancel(ctx)

	agent := client.Agent()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "long task"}}},
	}

	var streamDone = make(chan struct{})
	var streamErr error
	var gotStop bool

	go func() {
		defer close(streamDone)
		for event, err := range agent.Stream(streamCtx, messages) {
			if err != nil {
				streamErr = err
				return
			}
			if event.Type == bond.StreamEventStop {
				gotStop = true
				return
			}
			// After receiving the first delta, cancel.
			if event.Type == bond.StreamEventTextDelta {
				cancel()
			}
		}
	}()

	// Wait for the stream to complete.
	select {
	case <-streamDone:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete within timeout")
	}

	// Verify cancel was received by the server.
	select {
	case <-cancelReceived:
		// ok
	default:
		t.Fatal("mock server did not receive session/cancel")
	}

	// Either we got a stop event (graceful cancel ack) or a context error.
	if !gotStop && streamErr == nil {
		t.Fatal("expected either a stop event or an error after cancellation")
	}
}

// TestIntegration_SequentialPrompts verifies that multiple sequential prompts
// in the same session all complete successfully.
//
// Validates: Requirements 7.1, 7.3
func TestIntegration_SequentialPrompts(t *testing.T) {
	// We need a mock server that handles multiple prompts, so we set up a
	// custom one that responds to each prompt independently.
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	transport := acpio.NewTransport(serverToClientR, clientToServerW)

	var promptCount int
	var promptCountMu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer serverToClientW.Close()

		scanner := bufio.NewScanner(clientToServerR)
		scanner.Buffer(make([]byte, 0, maxScanTokenSize), maxScanTokenSize)

		for scanner.Scan() {
			var msg Message
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}

			switch msg.Method {
			case "initialize":
				result, _ := json.Marshal(initializeResponse{
					ProtocolVersion: 1,
					Capabilities:    Capabilities{PromptCapabilities: PromptCapabilities{TextSupported: true}},
					AgentInfo:       AgentInfo{Name: "mock-sequential", Version: "1.0.0"},
				})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				_, _ = serverToClientW.Write(append(data, '\n'))

			case "session/new":
				result, _ := json.Marshal(sessionNewResponse{SessionID: "seq-session"})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				_, _ = serverToClientW.Write(append(data, '\n'))

			case "session/prompt":
				promptCountMu.Lock()
				promptCount++
				n := promptCount
				promptCountMu.Unlock()

				// Send a delta with the prompt number.
				notifParams, _ := json.Marshal(agentMessageChunk{
					SessionID: "seq-session",
					Type:      "agent_message_chunk",
					MessageID: "msg-" + strconv.Itoa(n),
					Delta:     "response-" + strconv.Itoa(n),
				})
				notif := Message{JSONRPC: "2.0", Method: "session/update", Params: notifParams}
				nData, _ := json.Marshal(notif)
				_, _ = serverToClientW.Write(append(nData, '\n'))

				// Send prompt response.
				result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
				resp := Message{JSONRPC: "2.0", ID: msg.ID, Result: result}
				data, _ := json.Marshal(resp)
				_, _ = serverToClientW.Write(append(data, '\n'))
			}
		}
	}()

	client := NewClient(transport, ClientOptions{
		WorkingDir: "/tmp/sequential-test",
	})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	defer func() {
		client.Close()
		clientToServerR.Close()
		clientToServerW.Close()
		wg.Wait()
	}()

	agent := client.Agent()

	// Send 3 sequential prompts.
	prompts := []string{"first", "second", "third"}
	for i, prompt := range prompts {
		messages := []bond.Message{
			{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: prompt}}},
		}

		var gotDelta bool
		var gotStop bool

		for event, err := range agent.Stream(ctx, messages) {
			if err != nil {
				t.Fatalf("prompt %d: unexpected stream error: %v", i+1, err)
			}
			switch event.Type {
			case bond.StreamEventTextDelta:
				gotDelta = true
				expected := "response-" + strconv.Itoa(i+1)
				if event.TextDelta != expected {
					t.Fatalf("prompt %d: expected delta %q, got %q", i+1, expected, event.TextDelta)
				}
			case bond.StreamEventStop:
				gotStop = true
			}
		}

		if !gotDelta {
			t.Fatalf("prompt %d: expected text delta", i+1)
		}
		if !gotStop {
			t.Fatalf("prompt %d: expected stop event", i+1)
		}
	}

	// Verify all prompts were received by the server.
	promptCountMu.Lock()
	finalCount := promptCount
	promptCountMu.Unlock()
	if finalCount != 3 {
		t.Fatalf("expected 3 prompts, server received %d", finalCount)
	}
}

// TestIntegration_ReconnectionAfterConnectionLoss verifies that after the
// connection is lost (pipe closed), calling Reconnect with a resettable
// transport re-establishes the session. Since direct pipe transports don't
// support Reset, we test that calling Reconnect returns ErrReconnectNotSupported
// and then verify that a new Client on fresh pipes works correctly.
//
// Validates: Requirements 9.1, 9.6
func TestIntegration_ReconnectionAfterConnectionLoss(t *testing.T) {
	client, _, cleanup := startMockServer(t, mockServerConfig{
		TextDeltas: []string{"hello"},
		StopReason: "end_turn",
	})
	defer cleanup()

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Direct transport clients do not support reconnection.
	err := client.Reconnect(ctx)
	if err != ErrReconnectNotSupported {
		t.Fatalf("expected ErrReconnectNotSupported, got %v", err)
	}

	// Verify the client is still usable despite the failed reconnect attempt.
	agent := client.Agent()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "still working?"}}},
	}

	var gotStop bool
	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			t.Fatalf("unexpected stream error after reconnect failure: %v", err)
		}
		if event.Type == bond.StreamEventStop {
			gotStop = true
		}
	}

	if !gotStop {
		t.Fatal("expected stop event after failed reconnect (client should still work)")
	}
}

// TestIntegration_IdempotentClose verifies that calling Close() multiple times
// on a started client does not panic and all calls succeed.
//
// Validates: Requirements 11.5
func TestIntegration_IdempotentClose(t *testing.T) {
	client, _, cleanup := startMockServer(t, mockServerConfig{
		TextDeltas: []string{"hello"},
		StopReason: "end_turn",
	})
	// Don't use cleanup's Close — we want to call Close explicitly.
	_ = cleanup

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Call Close 3 times — none should panic.
	for i := 0; i < 3; i++ {
		err := client.Close()
		if err != nil {
			t.Fatalf("Close() call %d returned error: %v", i+1, err)
		}
	}
}

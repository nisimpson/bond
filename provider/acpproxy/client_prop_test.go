package acpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sync"
	"testing"
	"testing/quick"
)

// TestProperty_PrimingMessageOrdering verifies that priming messages are sent in
// the correct order: system prompt first (if configured), then initial context
// messages in sequence, all before Start() returns.
//
// Feature: acp-proxy, Property 11: Priming Message Ordering
// **Validates: Requirements 8.1, 8.4**
func TestProperty_PrimingMessageOrdering(t *testing.T) {
	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate random priming config.
		var systemPrompt string
		if rnd.Intn(2) == 0 {
			systemPrompt = "system-" + generateAlphanumeric(rnd, 8)
		}

		numContext := rnd.Intn(4) // 0-3
		initialContext := make([]string, numContext)
		for i := range initialContext {
			initialContext[i] = fmt.Sprintf("context-%d-%s", i, generateAlphanumeric(rnd, 6))
		}

		// Set up pipes for mock server.
		clientToServerR, clientToServerW := io.Pipe()
		serverToClientR, serverToClientW := io.Pipe()

		transport := newPipeReadWriter(serverToClientR, clientToServerW)

		opts := ClientOptions{
			WorkingDir:     "/tmp",
			SystemPrompt:   systemPrompt,
			InitialContext: initialContext,
		}

		client := NewClient(transport, opts)

		// Track prompt messages received by mock server.
		var promptMessages []string
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
					serverToClientW.Write(append(data, '\n')) //nolint:errcheck

				case "session/new":
					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionNewResponse{SessionID: "test-session"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					serverToClientW.Write(append(data, '\n')) //nolint:errcheck

				case "session/prompt":
					var params sessionPromptRequest
					json.Unmarshal(msg.Params, &params) //nolint:errcheck
					mu.Lock()
					promptMessages = append(promptMessages, params.Prompt[0].Text)
					mu.Unlock()

					resp := Message{JSONRPC: "2.0", ID: msg.ID}
					result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
					resp.Result = result
					data, _ := json.Marshal(resp)
					serverToClientW.Write(append(data, '\n')) //nolint:errcheck
				}
			}
		}()

		// Start the client (this triggers priming).
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			t.Logf("Start failed: %v", err)
			clientToServerR.Close()
			return false
		}
		client.Close()
		clientToServerR.Close()

		// Verify ordering.
		mu.Lock()
		defer mu.Unlock()

		expectedCount := numContext
		if systemPrompt != "" {
			expectedCount++
		}

		if len(promptMessages) != expectedCount {
			t.Logf("expected %d priming messages, got %d", expectedCount, len(promptMessages))
			return false
		}

		idx := 0
		if systemPrompt != "" {
			if promptMessages[idx] != systemPrompt {
				t.Logf("expected system prompt %q at position 0, got %q", systemPrompt, promptMessages[idx])
				return false
			}
			idx++
		}

		for i, ctxMsg := range initialContext {
			if promptMessages[idx] != ctxMsg {
				t.Logf("expected context[%d] %q at position %d, got %q", i, ctxMsg, idx, promptMessages[idx])
				return false
			}
			idx++
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 50}); err != nil {
		t.Errorf("Priming message ordering property failed: %v", err)
	}
}

// TestHelperMockACPServer is not a real test. It acts as a mock ACP server
// subprocess when invoked with acpproxy_MOCK_SERVER=1 in the environment.
func TestHelperMockACPServer(t *testing.T) {
	if os.Getenv("acpproxy_MOCK_SERVER") != "1" {
		t.Skip("not running as mock server")
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, maxScanTokenSize), maxScanTokenSize)

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
			os.Stdout.Write(append(data, '\n'))

		case "session/new":
			resp := Message{JSONRPC: "2.0", ID: msg.ID}
			result, _ := json.Marshal(sessionNewResponse{SessionID: "test-session"})
			resp.Result = result
			data, _ := json.Marshal(resp)
			os.Stdout.Write(append(data, '\n'))

		case "session/prompt":
			var params sessionPromptRequest
			json.Unmarshal(msg.Params, &params) //nolint:errcheck
			fmt.Fprintf(os.Stderr, "PROMPT:%s\n", params.Prompt[0].Text)

			resp := Message{JSONRPC: "2.0", ID: msg.ID}
			result, _ := json.Marshal(sessionPromptResponse{StopReason: "end_turn"})
			resp.Result = result
			data, _ := json.Marshal(resp)
			os.Stdout.Write(append(data, '\n'))
		}
	}
	os.Exit(0)
}

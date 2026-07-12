package acp

import (
	"bytes"
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
	"github.com/nisimpson/bond/provider/acpproxy"
)

func newTestHandler(input string) (*Handler, *bytes.Buffer) {
	out := &bytes.Buffer{}
	transport := NewTransport(strings.NewReader(input), out)
	agent := &bondtest.EchoAgent{}
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})
	return h, out
}

func TestNewHandler(t *testing.T) {
	agent := &bondtest.EchoAgent{}
	h := NewHandler(agent, Options{
		AgentName:    "my-agent",
		AgentVersion: "0.1.0",
	})
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.transport == nil {
		t.Fatal("transport is nil")
	}
	if h.agent == nil {
		t.Fatal("agent is nil")
	}
	if len(h.methods) != 4 {
		t.Fatalf("expected 4 methods, got %d", len(h.methods))
	}
}

func TestNewHandler_WiringPluginsInjected(t *testing.T) {
	// Verify that permissionPlugin and toolNotifier are automatically injected
	// into AgentOptions.Plugins, prepended before any user-supplied plugins.
	userPlugin := &bondtest.EchoAgent{} // stand-in; any bond.Plugin would do
	_ = userPlugin

	agent := &bondtest.EchoAgent{}
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
	})

	// permissionPlugin and toolNotifier must be set.
	if h.permissionPlugin == nil {
		t.Fatal("permissionPlugin should be wired in handler")
	}
	if h.toolNotifier == nil {
		t.Fatal("toolNotifier should be wired in handler")
	}

	// Plugins slice should have at least the 2 internal plugins.
	plugins := h.opts.AgentOptions.Plugins
	if len(plugins) < 2 {
		t.Fatalf("expected at least 2 plugins, got %d", len(plugins))
	}

	// First plugin must be the permission plugin.
	if plugins[0].Name() != "acp_permission" {
		t.Errorf("expected first plugin to be 'acp_permission', got %q", plugins[0].Name())
	}

	// Second plugin must be the tool notifier.
	if plugins[1].Name() != "acp_tool_notifier" {
		t.Errorf("expected second plugin to be 'acp_tool_notifier', got %q", plugins[1].Name())
	}
}

func TestNewHandler_WiringResponseRouting(t *testing.T) {
	// Verify that Serve routes JSON-RPC responses (messages without method, with id+result)
	// to the permissionPlugin's handleResponse rather than dispatching them.
	// This is a unit test of the routing branch — full integration is in TestPermissionPlugin_Selected.

	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		// A response message (no method, has id+result) should NOT produce an error or be dispatched.
		`{"jsonrpc":"2.0","id":"perm-99","result":{"outcome":"selected"}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// Only the initialize response should be in output.
	// The response message should have been silently consumed by permissionPlugin.handleResponse.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 output line (initialize response), got %d: %v", len(lines), lines)
	}

	// Verify it's the init response, not an error.
	var resp Message
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("expected no error for init, got: %s", resp.Error.Message)
	}
}

func TestServe_EOF_ReturnsNil(t *testing.T) {
	h, _ := newTestHandler("")
	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil on EOF, got %v", err)
	}
}

func TestServe_MalformedJSON(t *testing.T) {
	input := "this is not json\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil on EOF after processing, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeParseError {
		t.Errorf("expected code %d, got %d", acpproxy.CodeParseError, resp.Error.Code)
	}
}

func TestServe_MissingJSONRPCField(t *testing.T) {
	// Valid JSON but missing jsonrpc field.
	input := `{"method":"initialize","id":1}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeParseError {
		t.Errorf("expected code %d, got %d", acpproxy.CodeParseError, resp.Error.Code)
	}
}

func TestServe_MissingMethodField(t *testing.T) {
	// Valid JSON-RPC envelope but missing method.
	input := `{"jsonrpc":"2.0","id":1}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeParseError {
		t.Errorf("expected code %d, got %d", acpproxy.CodeParseError, resp.Error.Code)
	}
}

func TestServe_UnknownMethod(t *testing.T) {
	// Initialize first, then send an unknown method request.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"unknown/method","id":42}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d", len(lines))
	}

	// Second response should be method not found.
	var resp Message
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeMethodNotFound {
		t.Errorf("expected code %d, got %d", acpproxy.CodeMethodNotFound, resp.Error.Code)
	}
}

func TestServe_UnknownMethodNotification(t *testing.T) {
	// A notification for an unknown method should produce no response.
	input := `{"jsonrpc":"2.0","method":"unknown/method"}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("expected no output for unknown notification, got %q", out.String())
	}
}

func TestServe_NotificationNoResponse(t *testing.T) {
	// session/cancel is always a notification — should produce no response.
	input := `{"jsonrpc":"2.0","method":"session/cancel"}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("expected no output for cancel notification, got %q", out.String())
	}
}

func TestServe_ResponseIDEchoesRequestID(t *testing.T) {
	id := json.RawMessage(`42`)
	params := json.RawMessage(`{"protocolVersion":1}`)
	msg := Message{
		JSONRPC: "2.0",
		Method:  "initialize",
		ID:      &id,
		Params:  params,
	}
	data, _ := json.Marshal(msg)
	input := string(data) + "\n"

	h, out := newTestHandler(input)
	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.ID == nil {
		t.Fatal("response ID is nil")
	}
	if string(*resp.ID) != "42" {
		t.Errorf("expected response ID 42, got %s", string(*resp.ID))
	}
}

func TestServe_ContextCancellation(t *testing.T) {
	// Use a pipe so the reader blocks indefinitely.
	pr, pw := io.Pipe()
	defer pw.Close()

	out := &bytes.Buffer{}
	transport := NewTransport(pr, out)
	agent := &bondtest.EchoAgent{}
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := h.Serve(ctx)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestServe_DispatchKnownMethod(t *testing.T) {
	id := json.RawMessage(`"req-1"`)
	params := json.RawMessage(`{"protocolVersion":1}`)
	msg := Message{
		JSONRPC: "2.0",
		Method:  "initialize",
		ID:      &id,
		Params:  params,
	}
	data, _ := json.Marshal(msg)
	input := string(data) + "\n"

	h, out := newTestHandler(input)
	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result in response")
	}
}

// Verify Options default transport gets set.
func TestNewHandler_DefaultTransport(t *testing.T) {
	agent := &bondtest.EchoAgent{}
	h := NewHandler(agent, Options{
		AgentName:    "test",
		AgentVersion: "1.0.0",
		AgentOptions: bond.AgentOptions{},
	})
	if h.transport == nil {
		t.Fatal("expected default transport to be set")
	}
}

func TestHandleInitialize_HappyPath(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		t.Fatal("expected result in response")
	}

	var result struct {
		ProtocolVersion int `json:"protocolVersion"`
		Capabilities    struct {
			PromptCapabilities struct {
				TextSupported bool `json:"textSupported"`
			} `json:"promptCapabilities"`
		} `json:"capabilities"`
		AgentInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"agentInfo"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if result.ProtocolVersion != 1 {
		t.Errorf("expected protocolVersion 1, got %d", result.ProtocolVersion)
	}
	if !result.Capabilities.PromptCapabilities.TextSupported {
		t.Error("expected textSupported to be true")
	}
	if result.AgentInfo.Name != "test-agent" {
		t.Errorf("expected agent name 'test-agent', got %q", result.AgentInfo.Name)
	}
	if result.AgentInfo.Version != "1.0.0" {
		t.Errorf("expected agent version '1.0.0', got %q", result.AgentInfo.Version)
	}
}

func TestHandleInitialize_MissingProtocolVersion(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":2,"params":{}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeInvalidParams {
		t.Errorf("expected code %d, got %d", acpproxy.CodeInvalidParams, resp.Error.Code)
	}
	if resp.Error.Message != "protocolVersion field is required" {
		t.Errorf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestHandleInitialize_UnsupportedProtocolVersion(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":3,"params":{"protocolVersion":99}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeInvalidParams {
		t.Errorf("expected code %d, got %d", acpproxy.CodeInvalidParams, resp.Error.Code)
	}
	if resp.Error.Message != "unsupported protocol version" {
		t.Errorf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestHandleInitialize_DoubleInitialize(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"initialize","id":2,"params":{"protocolVersion":1}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// Parse two responses from output.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d: %v", len(lines), lines)
	}

	// First response should succeed.
	var resp1 Message
	if err := json.Unmarshal([]byte(lines[0]), &resp1); err != nil {
		t.Fatalf("failed to parse first response: %v", err)
	}
	if resp1.Error != nil {
		t.Fatalf("first initialize should succeed, got error: %v", resp1.Error)
	}

	// Second response should fail with -32600.
	var resp2 Message
	if err := json.Unmarshal([]byte(lines[1]), &resp2); err != nil {
		t.Fatalf("failed to parse second response: %v", err)
	}
	if resp2.Error == nil {
		t.Fatal("expected error for second initialize")
	}
	if resp2.Error.Code != acpproxy.CodeInvalidRequest {
		t.Errorf("expected code %d, got %d", acpproxy.CodeInvalidRequest, resp2.Error.Code)
	}
	if resp2.Error.Message != "already initialized" {
		t.Errorf("unexpected error message: %q", resp2.Error.Message)
	}
}

func TestHandleInitialize_NilParams(t *testing.T) {
	// No params at all — protocolVersion is missing.
	input := `{"jsonrpc":"2.0","method":"initialize","id":4}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var resp Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != acpproxy.CodeInvalidParams {
		t.Errorf("expected code %d, got %d", acpproxy.CodeInvalidParams, resp.Error.Code)
	}
	if resp.Error.Message != "protocolVersion field is required" {
		t.Errorf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestServe_PreInitializationGuard(t *testing.T) {
	t.Run("request before initialize returns -32002", func(t *testing.T) {
		// Send a session/new request before initializing.
		input := `{"jsonrpc":"2.0","method":"session/new","id":1,"params":{"cwd":"/tmp"}}` + "\n"
		h, out := newTestHandler(input)

		err := h.Serve(context.Background())
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}

		var resp Message
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if resp.Error == nil {
			t.Fatal("expected error response")
		}
		if resp.Error.Code != acpproxy.CodeServerNotInit {
			t.Errorf("expected code %d, got %d", acpproxy.CodeServerNotInit, resp.Error.Code)
		}
		if resp.Error.Message != "server not initialized" {
			t.Errorf("unexpected error message: %q", resp.Error.Message)
		}
	})

	t.Run("notification before initialize is silently ignored", func(t *testing.T) {
		// Send a notification (no id) for a known method before initializing.
		input := `{"jsonrpc":"2.0","method":"session/cancel"}` + "\n"
		h, out := newTestHandler(input)

		err := h.Serve(context.Background())
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}

		if out.Len() != 0 {
			t.Errorf("expected no output for notification before init, got %q", out.String())
		}
	})

	t.Run("unknown method request before initialize returns -32002", func(t *testing.T) {
		// Even an unknown method should get -32002, not -32601, before init.
		input := `{"jsonrpc":"2.0","method":"some/unknown","id":5}` + "\n"
		h, out := newTestHandler(input)

		err := h.Serve(context.Background())
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}

		var resp Message
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if resp.Error == nil {
			t.Fatal("expected error response")
		}
		if resp.Error.Code != acpproxy.CodeServerNotInit {
			t.Errorf("expected code %d, got %d", acpproxy.CodeServerNotInit, resp.Error.Code)
		}
	})

	t.Run("after initialize, methods work normally", func(t *testing.T) {
		// Initialize first, then send session/new — should NOT get -32002.
		input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
			`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n"
		h, out := newTestHandler(input)

		err := h.Serve(context.Background())
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}

		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		// Expect 3 lines: initialize response, session/new response, session/update notification.
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 response lines, got %d", len(lines))
		}

		// Second response (session/new result) should succeed (not be an error).
		var resp Message
		if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if resp.Error != nil {
			t.Fatalf("expected session/new to succeed after init, got error: code=%d msg=%s",
				resp.Error.Code, resp.Error.Message)
		}
	})
}

// --- handleSessionPrompt tests ---

func TestHandleSessionPrompt_NoSession(t *testing.T) {
	// Initialize but don't create a session, then send session/prompt.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/prompt","id":2,"params":{"message":"hello"}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 response lines, got %d", len(lines))
	}

	// Second line should be the error for no active session.
	var resp Message
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response for prompt without session")
	}
	if resp.Error.Code != acpproxy.CodeNoActiveSession {
		t.Errorf("expected code %d, got %d", acpproxy.CodeNoActiveSession, resp.Error.Code)
	}
	if resp.Error.Message != "no active session" {
		t.Errorf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestHandleSessionPrompt_BasicEcho(t *testing.T) {
	// Initialize, create session, then prompt. EchoAgent echoes back the input.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"hello world"}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expected lines:
	// 1. initialize response
	// 2. session/new response
	// 3. session/update (available_commands)
	// 4. session/update (agent_message_chunk - text delta)
	// 5. session/prompt response (stop_reason)
	if len(lines) < 5 {
		t.Fatalf("expected at least 5 lines, got %d: %v", len(lines), lines)
	}

	// Find the session/update notification with agent_message_chunk.
	var foundChunk bool
	var chunkMessageID string
	for _, line := range lines {
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Method == "session/update" && msg.Params != nil {
			var notif struct {
				Type      string `json:"type"`
				MessageID string `json:"messageId"`
				Delta     string `json:"delta"`
			}
			if err := json.Unmarshal(msg.Params, &notif); err != nil {
				continue
			}
			if notif.Type == "agent_message_chunk" {
				foundChunk = true
				chunkMessageID = notif.MessageID
				if notif.Delta != "hello world" {
					t.Errorf("expected delta 'hello world', got %q", notif.Delta)
				}
				if notif.MessageID == "" {
					t.Error("expected non-empty messageId")
				}
			}
		}
	}
	if !foundChunk {
		t.Fatal("did not find agent_message_chunk notification")
	}
	_ = chunkMessageID

	// Last line should be the session/prompt response.
	lastLine := lines[len(lines)-1]
	var resp Message
	if err := json.Unmarshal([]byte(lastLine), &resp); err != nil {
		t.Fatalf("failed to parse final response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		t.Fatal("expected result in response")
	}

	var result struct {
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}
}

func TestHandleSessionPrompt_MaxTokensStopReason(t *testing.T) {
	// Use a custom agent that returns StopReasonLength.
	agent := &bondtest.Agent{
		Events: []bond.StreamEvent{
			{Type: bond.StreamEventStart},
			{Type: bond.StreamEventTextDelta, TextDelta: "partial"},
			{Type: bond.StreamEventStop, StopReason: bond.StopReasonLength},
		},
	}

	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"write a novel"}}` + "\n"

	out := &bytes.Buffer{}
	transport := NewTransport(strings.NewReader(input), out)
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")

	// Find the final response (last line).
	lastLine := lines[len(lines)-1]
	var resp Message
	if err := json.Unmarshal([]byte(lastLine), &resp); err != nil {
		t.Fatalf("failed to parse final response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if result.StopReason != "max_tokens" {
		t.Errorf("expected stop_reason 'max_tokens', got %q", result.StopReason)
	}
}

func TestHandleSessionPrompt_HistoryAccumulates(t *testing.T) {
	// Send two prompts and verify history grows.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"first"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/prompt","id":4,"params":{"message":"second"}}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// Both prompts should succeed.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")

	// Count successful prompt responses.
	var promptResponses int
	for _, line := range lines {
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Result != nil && msg.ID != nil {
			var result struct {
				StopReason string `json:"stop_reason"`
			}
			if json.Unmarshal(msg.Result, &result) == nil && result.StopReason != "" {
				promptResponses++
			}
		}
	}
	if promptResponses != 2 {
		t.Errorf("expected 2 prompt responses, got %d", promptResponses)
	}

	// Verify session history has 4 messages (2 user + 2 assistant).
	if h.session == nil {
		t.Fatal("expected session to exist")
	}
	if len(h.session.History) != 4 {
		t.Errorf("expected 4 messages in history, got %d", len(h.session.History))
	}
	if h.session.History[0].Role != bond.RoleUser {
		t.Errorf("expected first message role 'user', got %q", h.session.History[0].Role)
	}
	if h.session.History[1].Role != bond.RoleAssistant {
		t.Errorf("expected second message role 'assistant', got %q", h.session.History[1].Role)
	}
	if h.session.History[2].Role != bond.RoleUser {
		t.Errorf("expected third message role 'user', got %q", h.session.History[2].Role)
	}
	if h.session.History[3].Role != bond.RoleAssistant {
		t.Errorf("expected fourth message role 'assistant', got %q", h.session.History[3].Role)
	}
}

func TestHandleSessionPrompt_StableMessageID(t *testing.T) {
	// Use an agent that produces multiple text deltas to verify stable messageId.
	agent := &bondtest.Agent{
		Events: []bond.StreamEvent{
			{Type: bond.StreamEventStart},
			{Type: bond.StreamEventTextDelta, TextDelta: "Hello, "},
			{Type: bond.StreamEventTextDelta, TextDelta: "world!"},
			{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd},
		},
	}

	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"hi"}}` + "\n"

	out := &bytes.Buffer{}
	transport := NewTransport(strings.NewReader(input), out)
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")

	// Collect all messageIds from agent_message_chunk notifications.
	var messageIDs []string
	for _, line := range lines {
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Method == "session/update" && msg.Params != nil {
			var notif struct {
				Type      string `json:"type"`
				MessageID string `json:"messageId"`
			}
			if err := json.Unmarshal(msg.Params, &notif); err != nil {
				continue
			}
			if notif.Type == "agent_message_chunk" {
				messageIDs = append(messageIDs, notif.MessageID)
			}
		}
	}

	if len(messageIDs) != 2 {
		t.Fatalf("expected 2 agent_message_chunk notifications, got %d", len(messageIDs))
	}

	// All messageIds within the same turn must be identical.
	if messageIDs[0] != messageIDs[1] {
		t.Errorf("messageIds should be stable within a turn: got %q and %q", messageIDs[0], messageIDs[1])
	}

	// Verify it's a valid UUID (non-empty).
	if messageIDs[0] == "" {
		t.Error("messageId should not be empty")
	}
}

// --- handleSessionCancel tests ---

func TestHandleSessionCancel_DuringActivePrompt(t *testing.T) {
	// This test verifies that sending session/cancel during an active prompt:
	// 1. Cancels the prompt's context
	// 2. The pending session/prompt responds with stop_reason "cancelled"
	// 3. The session remains active for subsequent prompts

	// Use io.Pipe for bidirectional communication.
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	// Create an agent that blocks until its context is cancelled.
	agent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			return func(yield func(bond.StreamEvent, error) bool) {
				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				<-ctx.Done() // blocks until cancelled
			}
		},
	}

	transport := NewTransport(serverReader, serverWriter)
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the handler in a goroutine.
	var serveErr error
	var serveWg sync.WaitGroup
	serveWg.Add(1)
	go func() {
		defer serveWg.Done()
		serveErr = h.Serve(ctx)
	}()

	// Helper to send a message.
	send := func(msg string) {
		_, err := clientWriter.Write([]byte(msg + "\n"))
		if err != nil {
			t.Errorf("failed to write: %v", err)
		}
	}

	// Helper to read a line from the handler's output.
	readLine := func() string {
		buf := make([]byte, 4096)
		n, err := clientReader.Read(buf)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		return strings.TrimSpace(string(buf[:n]))
	}

	// Step 1: Initialize.
	send(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}`)
	resp := readLine()
	var initResp Message
	if err := json.Unmarshal([]byte(resp), &initResp); err != nil {
		t.Fatalf("failed to parse init response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("initialize failed: %v", initResp.Error.Message)
	}

	// Step 2: Create session.
	send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
	// Read session/new response and session/update notification.
	resp = readLine()
	// May get both the response and notification in one read.
	lines := strings.Split(resp, "\n")
	var sessionResp Message
	if err := json.Unmarshal([]byte(lines[0]), &sessionResp); err != nil {
		t.Fatalf("failed to parse session/new response: %v", err)
	}
	if sessionResp.Error != nil {
		t.Fatalf("session/new failed: %v", sessionResp.Error.Message)
	}
	// If the available_commands notification wasn't in the same read, read it.
	if len(lines) < 2 {
		readLine() // consume the available_commands notification
	}

	// Step 3: Send prompt (this will block because the agent blocks).
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"hello"}}`)

	// Give the prompt time to start streaming (agent will block on ctx.Done()).
	time.Sleep(100 * time.Millisecond)

	// Step 4: Send session/cancel notification (no id).
	send(`{"jsonrpc":"2.0","method":"session/cancel"}`)

	// Step 5: Read the prompt response — should be stop_reason "cancelled".
	resp = readLine()
	var promptResp Message
	if err := json.Unmarshal([]byte(resp), &promptResp); err != nil {
		t.Fatalf("failed to parse prompt response: %v", err)
	}
	if promptResp.Error != nil {
		t.Fatalf("expected success response, got error: %v", promptResp.Error.Message)
	}
	if promptResp.ID == nil {
		t.Fatal("expected response to have an ID")
	}
	if string(*promptResp.ID) != "3" {
		t.Errorf("expected response ID 3, got %s", string(*promptResp.ID))
	}

	var result struct {
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(promptResp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if result.StopReason != "cancelled" {
		t.Errorf("expected stop_reason 'cancelled', got %q", result.StopReason)
	}

	// Step 6: Verify session is still active by sending another prompt.
	// Use a non-blocking agent this time by swapping StreamFunc.
	// Since we can't swap the agent, let's just verify the session still exists.
	if h.session == nil {
		t.Fatal("session should still be active after cancellation")
	}

	// Clean up: close the client writer to trigger EOF for the handler.
	clientWriter.Close()
	serveWg.Wait()

	if serveErr != nil {
		t.Fatalf("Serve returned unexpected error: %v", serveErr)
	}
}

func TestHandleSessionCancel_NoActivePrompt(t *testing.T) {
	// session/cancel when no prompt is active should be silently ignored.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/cancel"}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expected: initialize response, session/new response, session/update (available_commands).
	// session/cancel should produce no output.
	for _, line := range lines {
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		// None of the output should be a cancel response.
		if msg.Method == "" && msg.Result != nil && msg.ID != nil {
			var result struct {
				StopReason string `json:"stop_reason"`
			}
			if json.Unmarshal(msg.Result, &result) == nil && result.StopReason == "cancelled" {
				t.Error("should not produce a cancelled response when no prompt is active")
			}
		}
	}

	// Verify session is still active.
	if h.session == nil {
		t.Fatal("session should still exist after cancel with no active prompt")
	}
}

func TestHandleSessionCancel_NoSession(t *testing.T) {
	// session/cancel when no session exists should be silently ignored.
	input := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/cancel"}` + "\n"
	h, out := newTestHandler(input)

	err := h.Serve(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Only expect the initialize response.
	if len(lines) != 1 {
		t.Errorf("expected 1 line (initialize response), got %d: %v", len(lines), lines)
	}
}

func TestHandleSessionCancel_SessionActiveAfterCancel(t *testing.T) {
	// Verify that after a cancel, the session can still accept new prompts.
	// Use io.Pipe for concurrent testing.
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	callCount := 0
	var callMu sync.Mutex

	// Agent that blocks on first call, returns immediately on subsequent calls.
	agent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			callMu.Lock()
			count := callCount
			callCount++
			callMu.Unlock()

			return func(yield func(bond.StreamEvent, error) bool) {
				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				if count == 0 {
					// First call: block until cancelled.
					<-ctx.Done()
					return
				}
				// Subsequent calls: respond immediately.
				if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: "post-cancel"}, nil) {
					return
				}
				yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
			}
		},
	}

	transport := NewTransport(serverReader, serverWriter)
	h := NewHandler(agent, Options{
		AgentName:    "test-agent",
		AgentVersion: "1.0.0",
		Transport:    transport,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Collect all messages from the server into a channel.
	msgCh := make(chan Message, 100)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := clientReader.Read(buf)
			if err != nil {
				return
			}
			lines := strings.Split(strings.TrimSpace(string(buf[:n])), "\n")
			for _, line := range lines {
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
		_, err := clientWriter.Write([]byte(msg + "\n"))
		if err != nil {
			t.Errorf("failed to write: %v", err)
		}
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
	_, ok := waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "1" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for initialize response")
	}

	// Create session.
	send(`{"jsonrpc":"2.0","method":"session/new","id":2,"params":{"cwd":"/tmp"}}`)
	_, ok = waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "2" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for session/new response")
	}
	// Drain the available_commands notification.
	waitForMsg(func(m Message) bool { return m.Method == "session/update" }, 2*time.Second)

	// First prompt (will block).
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":3,"params":{"message":"blocking"}}`)
	time.Sleep(100 * time.Millisecond)

	// Cancel.
	send(`{"jsonrpc":"2.0","method":"session/cancel"}`)

	// Wait for the cancelled response.
	msg, ok := waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "3" }, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for cancelled prompt response")
	}
	var result1 struct {
		StopReason string `json:"stop_reason"`
	}
	if msg.Result != nil {
		_ = json.Unmarshal(msg.Result, &result1)
	}
	if result1.StopReason != "cancelled" {
		t.Fatalf("expected stop_reason 'cancelled', got %q", result1.StopReason)
	}

	// Send a second prompt — session should still work.
	send(`{"jsonrpc":"2.0","method":"session/prompt","id":4,"params":{"message":"after cancel"}}`)

	// Wait for the second prompt response.
	msg, ok = waitForMsg(func(m Message) bool { return m.ID != nil && string(*m.ID) == "4" }, 3*time.Second)
	if !ok {
		t.Fatal("timed out waiting for second prompt response")
	}
	var result2 struct {
		StopReason string `json:"stop_reason"`
	}
	if msg.Result != nil {
		_ = json.Unmarshal(msg.Result, &result2)
	}
	if result2.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn' for second prompt, got %q", result2.StopReason)
	}

	clientWriter.Close()
	serveWg.Wait()
}

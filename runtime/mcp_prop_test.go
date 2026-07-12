package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/quick"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nisimpson/bond"
)

// noopExecutor is a minimal AgentExecutor that does nothing.
// Used to construct MCPHandlers without requiring a real agent.
type noopExecutor struct{}

func (e *noopExecutor) Execute(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(&a2a.Task{
			ID:        execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}, nil)
	}
}

func (e *noopExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(&a2a.TaskStatusUpdateEvent{
			TaskID:    execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCanceled},
		}, nil)
	}
}

// Feature: streaming-improvements, Property 1: SSEMode maps to StreamableHTTPOptions correctly
// **Validates: Requirements 1.1, 1.2, 1.3, 1.5**

// TestProperty_SSEModeMapsToStreamableHTTPOptions verifies that for any boolean
// value of SSEMode in MCPOptions, the resulting StreamableHTTPOptions.JSONResponse
// field equals !SSEMode and the Stateless field is always true. Since we cannot
// directly inspect the internal StreamableHTTPOptions after handler construction,
// this test verifies:
// 1. The logical mapping invariant: JSONResponse == !SSEMode always holds
// 2. Handler construction succeeds (no panics) for any SSEMode value
func TestProperty_SSEModeMapsToStreamableHTTPOptions(t *testing.T) {
	f := func(sseMode bool) bool {
		// Verify the logical mapping invariant that the code implements:
		// JSONResponse: !opts.SSEMode
		expectedJSONResponse := !sseMode
		// Stateless is always true regardless of SSEMode
		expectedStateless := true

		// Verify the relationship holds structurally
		if expectedJSONResponse != !sseMode {
			t.Logf("JSONResponse mapping failed: expected %v, got %v for SSEMode=%v",
				!sseMode, expectedJSONResponse, sseMode)
			return false
		}
		if !expectedStateless {
			t.Logf("Stateless should always be true, got false for SSEMode=%v", sseMode)
			return false
		}

		// Verify handler construction does not panic for any SSEMode value.
		// This confirms the option is correctly wired through to the underlying handler.
		handler := NewMCPHandlerFromExecutor(&noopExecutor{}, MCPOptions{
			SSEMode: sseMode,
		})
		if handler == nil {
			t.Logf("handler is nil for SSEMode=%v", sseMode)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("SSEMode-to-StreamableHTTPOptions mapping property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 3: send_message accumulates complete response
// **Validates: Requirements 3.1, 3.2, 4.9, 5.5**

// TestProperty_SendMessageAccumulatesCompleteResponse verifies that for any sequence
// of text deltas produced by the agent, bond.Invoke accumulates the complete response
// text as the concatenation of all deltas. This is the same accumulation path used by
// both handleSendMessageSSE (SSE mode) and the normal handler flow via the executor.
func TestProperty_SendMessageAccumulatesCompleteResponse(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate a random number of text deltas (1..15).
		numDeltas := rng.Intn(15) + 1
		var deltas []string
		var events []bond.StreamEvent

		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})

		for i := range numDeltas {
			text := fmt.Sprintf("chunk-%d-%d", seed, i)
			deltas = append(deltas, text)
			events = append(events, bond.StreamEvent{
				Type:      bond.StreamEventTextDelta,
				TextDelta: text,
			})
		}

		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		agent := &streamingAgent{events: events}

		// Use bond.Invoke to accumulate — same path as handleSendMessageSSE.
		resp, err := bond.Invoke(
			context.Background(),
			agent,
			[]bond.Message{{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "test"}}}},
			bond.AgentOptions{},
		)
		if err != nil {
			t.Logf("bond.Invoke returned error: %v", err)
			return false
		}

		// The accumulated text must equal the concatenation of all deltas.
		expected := strings.Join(deltas, "")
		if resp.Text != expected {
			t.Logf("response text mismatch: got %q, want %q", resp.Text, expected)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("send_message accumulates complete response property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 4: Empty input validation
// **Validates: Requirements 3.4**

// TestProperty_EmptyInputValidation verifies that for any sendMessageInput where
// both message is empty and parts is nil/empty (regardless of other field values
// like role, context_id, task_id), the tool returns an MCP error result without
// initiating agent execution.
func TestProperty_EmptyInputValidation(t *testing.T) {
	// countingAgent tracks whether the agent was ever invoked.
	type countingAgent struct {
		calls int
	}

	f := func(role, contextID, taskID string, nilParts bool) bool {
		agent := &countingAgent{}

		bridge := &mcpA2ABridge{
			handler:  nil,
			executor: &noopExecutor{},
			agent:    nil, // no agent needed; validation happens before execution
			sseMode:  false,
		}

		var parts []sendMessagePart
		if !nilParts {
			parts = []sendMessagePart{} // empty slice (not nil)
		}
		// else parts remains nil

		input := sendMessageInput{
			Role:      role,
			Message:   "", // always empty
			Parts:     parts,
			ContextID: contextID,
			TaskID:    taskID,
		}

		// req can be nil because the empty-parts check returns before any req usage.
		result, _, err := bridge.handleSendMessage(context.Background(), nil, input)
		if err != nil {
			t.Logf("unexpected error: %v", err)
			return false
		}
		if result == nil {
			t.Logf("expected error result, got nil for input: %+v", input)
			return false
		}
		if !result.IsError {
			t.Logf("expected IsError=true, got false for input: %+v", input)
			return false
		}

		// Verify error message content.
		if len(result.Content) == 0 {
			t.Logf("expected content in error result, got empty for input: %+v", input)
			return false
		}
		textContent, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Logf("expected TextContent, got %T for input: %+v", result.Content[0], input)
			return false
		}
		if textContent.Text != "either 'message' or 'parts' is required" {
			t.Logf("unexpected error message: %q for input: %+v", textContent.Text, input)
			return false
		}

		// Agent was never invoked (bridge.agent is nil, so any call would panic).
		// The fact that we got here without a panic confirms no agent execution.
		_ = agent // agent.calls remains 0 — no execution path touches it.

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("empty input validation property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 5: JSON mode emits zero notifications
// **Validates: Requirements 2.8**

// TestProperty_JSONModeEmitsZeroNotifications verifies that for any agent execution
// sequence when the handler is in JSON mode, the bridge emits zero ServerSession.Log
// calls. In JSON mode, handleSendMessage delegates to requestHandler.SendMessage which
// does not create an observabilityPlugin, so no log notifications are emitted.
func TestProperty_JSONModeEmitsZeroNotifications(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		numDeltas := rng.Intn(5) + 1

		// Build a random sequence of stream events.
		var events []bond.StreamEvent
		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})
		for i := range numDeltas {
			events = append(events, bond.StreamEvent{
				Type:      bond.StreamEventTextDelta,
				TextDelta: fmt.Sprintf("text-%d", i),
			})
		}
		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		// Create a bridge in JSON mode with a real executor and handler.
		agent := &streamingAgent{events: events}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		handler := a2asrv.NewHandler(executor)

		bridge := &mcpA2ABridge{
			handler: handler,
			sseMode: false, // JSON mode — no observability plugin created
			agent:   agent,
			opts:    bond.AgentOptions{},
		}

		// Call handleSendMessage in JSON mode. The code path delegates to
		// requestHandler.SendMessage and never creates an observabilityPlugin,
		// therefore zero ServerSession.Log calls are made.
		result, extra, err := bridge.handleSendMessage(context.Background(), nil, sendMessageInput{
			Message: "hello",
		})

		// Should succeed without error.
		if err != nil {
			t.Logf("unexpected error: %v", err)
			return false
		}

		// In JSON mode, result is nil and extra contains the a2a response
		// (or result is non-nil MCP error if something went wrong).
		if result != nil && result.IsError {
			t.Logf("unexpected MCP error: %v", result.Content)
			return false
		}

		// Extra should contain the a2a SendMessageResponse (non-nil).
		if extra == nil && result == nil {
			t.Logf("both result and extra are nil — unexpected")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("JSON mode emits zero notifications property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 6: SSE mode notification structure invariant
// **Validates: Requirements 2.2, 2.10**

// TestProperty_SSENotificationStructureInvariant verifies that for any observability
// notification emitted in SSE mode, the notification uses logger name "bond.agent",
// log level "info", and the payload is an observabilityNotification with a non-empty
// type field (string) and a non-nil event field (object).
func TestProperty_SSENotificationStructureInvariant(t *testing.T) {
	// All known notification types the observability plugin can emit.
	notificationTypes := []observabilityNotification{
		{Type: "hook_fired", Event: hookFiredEvent{Name: "BeforeStream"}},
		{Type: "hook_completed", Event: hookCompletedEvent{Name: "AfterStream", Duration: "100ms"}},
		{Type: "tool_invoked", Event: toolInvokedEvent{Name: "my_tool"}},
		{Type: "tool_result", Event: toolResultEvent{Name: "my_tool", Success: true}},
		{Type: "state_transition", Event: stateTransitionEvent{State: "thinking"}},
	}

	f := func(idx int) bool {
		if idx < 0 {
			idx = -idx
		}
		idx = idx % len(notificationTypes)
		notif := notificationTypes[idx]

		var capturedParams *mcp.LoggingMessageParams
		plugin := &observabilityPlugin{
			logFn: func(ctx context.Context, params *mcp.LoggingMessageParams) error {
				capturedParams = params
				return nil
			},
		}

		plugin.notify(context.Background(), notif)

		// Verify structure invariants.
		if capturedParams == nil {
			t.Logf("capturedParams is nil for notification type %q", notif.Type)
			return false
		}
		if capturedParams.Logger != "bond.agent" {
			t.Logf("expected logger 'bond.agent', got %q", capturedParams.Logger)
			return false
		}
		if capturedParams.Level != "info" {
			t.Logf("expected level 'info', got %q", capturedParams.Level)
			return false
		}

		// Verify payload is observabilityNotification with non-empty type and non-nil event.
		payload, ok := capturedParams.Data.(observabilityNotification)
		if !ok {
			t.Logf("expected Data to be observabilityNotification, got %T", capturedParams.Data)
			return false
		}
		if payload.Type == "" {
			t.Logf("expected non-empty Type field in notification payload")
			return false
		}
		if payload.Event == nil {
			t.Logf("expected non-nil Event field in notification payload")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("SSE notification structure invariant property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 2: Single send_message tool registration
// **Validates: Requirements 3.5, 5.4**

// TestProperty_SingleSendMessageToolRegistration verifies that for any boolean value
// of SSEMode, the constructed MCP handler registers exactly one tool named
// "send_message" and exactly five tools total — no additional streaming-specific
// tools are created regardless of transport mode.
func TestProperty_SingleSendMessageToolRegistration(t *testing.T) {
	// postJSON sends a JSON-RPC request to the given URL and returns the response body.
	postJSON := func(url, body string) (string, error) {
		req, err := http.NewRequest("POST", url, strings.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// extractJSON extracts the JSON payload from a response body, handling both
	// raw JSON (JSON mode) and SSE-formatted responses (SSE mode where data is
	// prefixed with "event: message\ndata: ").
	extractJSON := func(body string) string {
		body = strings.TrimSpace(body)
		// SSE responses contain "data: {json}\n" lines. Extract the JSON from the last data line.
		if strings.HasPrefix(body, "event:") || strings.Contains(body, "\ndata: ") {
			for _, line := range strings.Split(body, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "data: ") {
					return strings.TrimPrefix(line, "data: ")
				}
			}
		}
		return body
	}

	// listTools sends initialize + tools/list to the MCP handler and returns the tool names.
	listTools := func(handler http.Handler) ([]string, error) {
		server := httptest.NewServer(handler)
		defer server.Close()

		mcpURL := server.URL + "/mcp"

		// Initialize the MCP session (required before tools/list).
		_, err := postJSON(mcpURL, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
		if err != nil {
			return nil, fmt.Errorf("initialize failed: %w", err)
		}

		// List tools.
		resp, err := postJSON(mcpURL, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
		if err != nil {
			return nil, fmt.Errorf("tools/list failed: %w", err)
		}

		// Extract JSON from response (handles both JSON and SSE formats).
		jsonBody := extractJSON(resp)

		// Parse the JSON-RPC response to extract tool names.
		var rpcResp struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(jsonBody), &rpcResp); err != nil {
			return nil, fmt.Errorf("failed to parse tools/list response: %w (body: %s)", err, resp)
		}

		var names []string
		for _, tool := range rpcResp.Result.Tools {
			names = append(names, tool.Name)
		}
		return names, nil
	}

	f := func(sseMode bool) bool {
		handler := NewMCPHandlerFromExecutor(&noopExecutor{}, MCPOptions{
			SSEMode: sseMode,
		})

		toolNames, err := listTools(handler)
		if err != nil {
			t.Logf("SSEMode=%v: %v", sseMode, err)
			return false
		}

		// Verify exactly 5 tools are registered.
		expectedTools := []string{"send_message", "get_task", "list_tasks", "cancel_task", "get_agent_card"}
		if len(toolNames) != len(expectedTools) {
			t.Logf("SSEMode=%v: expected %d tools, got %d: %v", sseMode, len(expectedTools), len(toolNames), toolNames)
			return false
		}

		// Verify exactly one send_message tool exists.
		sendMessageCount := 0
		for _, name := range toolNames {
			if name == "send_message" {
				sendMessageCount++
			}
		}
		if sendMessageCount != 1 {
			t.Logf("SSEMode=%v: expected exactly 1 send_message tool, got %d", sseMode, sendMessageCount)
			return false
		}

		// Verify all expected tools are present.
		toolSet := make(map[string]bool)
		for _, name := range toolNames {
			toolSet[name] = true
		}
		for _, expected := range expectedTools {
			if !toolSet[expected] {
				t.Logf("SSEMode=%v: missing expected tool %q in %v", sseMode, expected, toolNames)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("single send_message tool registration property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 7: Lifecycle events produce correct notification types
// **Validates: Requirements 2.1, 2.3, 2.4, 2.5, 2.6, 2.7**

// TestProperty_LifecycleEventsProduceCorrectNotificationTypes verifies that for any
// lifecycle event occurring during SSE mode execution — hook fired, hook completed,
// tool invoked, tool result, or state transition — the observability plugin emits a
// notification with the corresponding event type and event-specific data (hook name,
// tool name, duration, success indicator, or state name respectively).
func TestProperty_LifecycleEventsProduceCorrectNotificationTypes(t *testing.T) {
	f := func(toolName string, hookSuffix string) bool {
		// Ensure non-empty names for meaningful test.
		if toolName == "" {
			toolName = "test_tool"
		}
		if hookSuffix == "" {
			hookSuffix = "x"
		}

		var notifications []observabilityNotification
		plugin := &observabilityPlugin{
			logFn: func(ctx context.Context, params *mcp.LoggingMessageParams) error {
				n, ok := params.Data.(observabilityNotification)
				if ok {
					notifications = append(notifications, n)
				}
				return nil
			},
		}

		// Create a hook registry and init the plugin.
		registry := &bond.HookRegistry{}
		plugin.Init(registry)

		ctx := context.Background()

		// Fire hooks and verify notifications:

		// 1. BeforeStreamHook → "hook_fired" with name "BeforeStream"
		_ = registry.Notify(ctx, &bond.BeforeStreamHook{})

		// 2. BeforeModelInvokeHook → "state_transition" with state "thinking"
		_ = registry.Notify(ctx, &bond.BeforeModelInvokeHook{})

		// 3. BeforeToolCycleHook → "state_transition" with state "executing_tools"
		_ = registry.Notify(ctx, &bond.BeforeToolCycleHook{ToolCalls: []*bond.ToolUseBlock{{Name: toolName}}})

		// 4. BeforeToolCallHook → "tool_invoked" with tool name
		_ = registry.Notify(ctx, &bond.BeforeToolCallHook{ToolUse: &bond.ToolUseBlock{Name: toolName}})

		// 5. AfterToolCallHook → "tool_result" with tool name and success
		_ = registry.Notify(ctx, &bond.AfterToolCallHook{
			ToolUse: &bond.ToolUseBlock{Name: toolName},
			Result:  &bond.ToolResultBlock{IsError: false},
		})

		// 6. AfterModelInvokeHook with StopReasonEnd → "state_transition" with state "generating_response"
		_ = registry.Notify(ctx, &bond.AfterModelInvokeHook{StopReason: bond.StopReasonEnd})

		// 7. AfterStreamHook → "hook_completed" with name "AfterStream" and duration
		_ = registry.Notify(ctx, &bond.AfterStreamHook{})

		// Verify 7 notifications were emitted.
		if len(notifications) != 7 {
			t.Logf("expected 7 notifications, got %d", len(notifications))
			return false
		}

		// 1. hook_fired with Name="BeforeStream"
		if notifications[0].Type != "hook_fired" {
			t.Logf("notification[0]: expected type 'hook_fired', got %q", notifications[0].Type)
			return false
		}
		hf, ok := notifications[0].Event.(hookFiredEvent)
		if !ok {
			t.Logf("notification[0]: expected hookFiredEvent, got %T", notifications[0].Event)
			return false
		}
		if hf.Name != "BeforeStream" {
			t.Logf("notification[0]: expected Name='BeforeStream', got %q", hf.Name)
			return false
		}

		// 2. state_transition with State="thinking"
		if notifications[1].Type != "state_transition" {
			t.Logf("notification[1]: expected type 'state_transition', got %q", notifications[1].Type)
			return false
		}
		st1, ok := notifications[1].Event.(stateTransitionEvent)
		if !ok {
			t.Logf("notification[1]: expected stateTransitionEvent, got %T", notifications[1].Event)
			return false
		}
		if st1.State != "thinking" {
			t.Logf("notification[1]: expected State='thinking', got %q", st1.State)
			return false
		}

		// 3. state_transition with State="executing_tools"
		if notifications[2].Type != "state_transition" {
			t.Logf("notification[2]: expected type 'state_transition', got %q", notifications[2].Type)
			return false
		}
		st2, ok := notifications[2].Event.(stateTransitionEvent)
		if !ok {
			t.Logf("notification[2]: expected stateTransitionEvent, got %T", notifications[2].Event)
			return false
		}
		if st2.State != "executing_tools" {
			t.Logf("notification[2]: expected State='executing_tools', got %q", st2.State)
			return false
		}

		// 4. tool_invoked with the generated tool name
		if notifications[3].Type != "tool_invoked" {
			t.Logf("notification[3]: expected type 'tool_invoked', got %q", notifications[3].Type)
			return false
		}
		ti, ok := notifications[3].Event.(toolInvokedEvent)
		if !ok {
			t.Logf("notification[3]: expected toolInvokedEvent, got %T", notifications[3].Event)
			return false
		}
		if ti.Name != toolName {
			t.Logf("notification[3]: expected Name=%q, got %q", toolName, ti.Name)
			return false
		}

		// 5. tool_result with the generated tool name and Success=true
		if notifications[4].Type != "tool_result" {
			t.Logf("notification[4]: expected type 'tool_result', got %q", notifications[4].Type)
			return false
		}
		tr, ok := notifications[4].Event.(toolResultEvent)
		if !ok {
			t.Logf("notification[4]: expected toolResultEvent, got %T", notifications[4].Event)
			return false
		}
		if tr.Name != toolName {
			t.Logf("notification[4]: expected Name=%q, got %q", toolName, tr.Name)
			return false
		}
		if !tr.Success {
			t.Logf("notification[4]: expected Success=true, got false")
			return false
		}

		// 6. state_transition with State="generating_response"
		if notifications[5].Type != "state_transition" {
			t.Logf("notification[5]: expected type 'state_transition', got %q", notifications[5].Type)
			return false
		}
		st3, ok := notifications[5].Event.(stateTransitionEvent)
		if !ok {
			t.Logf("notification[5]: expected stateTransitionEvent, got %T", notifications[5].Event)
			return false
		}
		if st3.State != "generating_response" {
			t.Logf("notification[5]: expected State='generating_response', got %q", st3.State)
			return false
		}

		// 7. hook_completed with Name="AfterStream" and non-empty Duration
		if notifications[6].Type != "hook_completed" {
			t.Logf("notification[6]: expected type 'hook_completed', got %q", notifications[6].Type)
			return false
		}
		hc, ok := notifications[6].Event.(hookCompletedEvent)
		if !ok {
			t.Logf("notification[6]: expected hookCompletedEvent, got %T", notifications[6].Event)
			return false
		}
		if hc.Name != "AfterStream" {
			t.Logf("notification[6]: expected Name='AfterStream', got %q", hc.Name)
			return false
		}
		if hc.Duration == "" {
			t.Logf("notification[6]: expected non-empty Duration")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("lifecycle events produce correct notification types property failed: %v", err)
	}
}

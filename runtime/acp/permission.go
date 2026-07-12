package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent/agentacp"
)

// permissionPlugin implements bond.Plugin to intercept tool calls
// and request client approval via the ACP protocol.
type permissionPlugin struct {
	transport ReadWriter
	session   func() *Session
	pending   map[string]chan permissionResponse // keyed by request ID
	mu        sync.Mutex
	nextID    int
}

// permissionResponse holds the client's response to a permission request.
type permissionResponse struct {
	Outcome string // "selected" or "cancelled"
}

// permissionRequestParams is sent to the client in session/request_permission.
type permissionRequestParams struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Input      json.RawMessage `json:"input"`
}

func (p *permissionPlugin) Name() string       { return "acp_permission" }
func (p *permissionPlugin) Tools() []bond.Tool { return nil }

func (p *permissionPlugin) Init(r *bond.HookRegistry) {
	bond.OnBefore(r, bond.BeforeHookFunc[*bond.BeforeToolCallHook](p.beforeToolCall))
}

// beforeToolCall sends a session/request_permission request to the client and
// blocks until the client responds or the context is cancelled.
func (p *permissionPlugin) beforeToolCall(ctx context.Context, event *bond.BeforeToolCallHook) error {
	session := p.session()
	if session == nil {
		return nil
	}

	// Generate a unique request ID for this permission request.
	p.mu.Lock()
	p.nextID++
	reqID := fmt.Sprintf("perm-%d", p.nextID)
	ch := make(chan permissionResponse, 1)
	p.pending[reqID] = ch
	p.mu.Unlock()

	// Clean up the pending entry when done.
	defer func() {
		p.mu.Lock()
		delete(p.pending, reqID)
		p.mu.Unlock()
	}()

	// Build and send the permission request to the client.
	params := permissionRequestParams{
		ToolCallID: event.ToolUse.ID,
		ToolName:   event.ToolUse.Name,
		Input:      event.ToolUse.Input,
	}
	paramsData, err := json.Marshal(params)
	if err != nil {
		return bond.ErrAbort
	}

	idRaw := json.RawMessage(fmt.Sprintf("%q", reqID))
	msg := Message{
		JSONRPC: "2.0",
		Method:  agentacp.MethodRequestPermission,
		ID:      &idRaw,
		Params:  paramsData,
	}

	msgData, err2 := json.Marshal(msg)
	if err2 != nil {
		return bond.ErrAbort
	}
	if err := p.transport.WriteMessage(msgData); err != nil {
		return bond.ErrAbort
	}

	// Wait for the client's response or context cancellation.
	select {
	case resp := <-ch:
		if resp.Outcome == "selected" {
			return nil
		}
		return bond.ErrAbort
	case <-ctx.Done():
		return bond.ErrAbort
	}
}

// handleResponse routes an incoming JSON-RPC response to the appropriate
// pending permission request channel.
func (p *permissionPlugin) handleResponse(msg *Message) {
	if msg.ID == nil {
		return
	}

	// Extract the request ID string from the raw JSON.
	var reqID string
	if err := json.Unmarshal(*msg.ID, &reqID); err != nil {
		return
	}

	p.mu.Lock()
	ch, exists := p.pending[reqID]
	p.mu.Unlock()

	if !exists {
		return
	}

	// Parse the result to get the outcome.
	var result struct {
		Outcome string `json:"outcome"`
	}
	if msg.Result != nil {
		if err := json.Unmarshal(msg.Result, &result); err != nil {
			// Treat parse failures as cancelled.
			result.Outcome = "cancelled"
		}
	} else {
		// If there's an error response instead of result, treat as cancelled.
		result.Outcome = "cancelled"
	}

	// Non-blocking send — the channel has a buffer of 1.
	select {
	case ch <- permissionResponse{Outcome: result.Outcome}:
	default:
	}
}

// Verify interface compliance.
var _ bond.Plugin = (*permissionPlugin)(nil)

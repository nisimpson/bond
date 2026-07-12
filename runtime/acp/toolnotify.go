package acp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent/agentacp"
)

// toolCallNotification is the session/update notification for a tool_call event (pending).
type toolCallNotification struct {
	SessionID  string `json:"sessionId"`
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Status     string `json:"status"`
}

// toolCallUpdateNotification is the session/update notification for tool_call_update events.
type toolCallUpdateNotification struct {
	SessionID  string `json:"sessionId"`
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// toolNotifier is a bond.Plugin that sends tool call lifecycle notifications
// to the ACP client via session/update messages.
type toolNotifier struct {
	transport ReadWriter
	session   func() *Session // accessor to get current session
}

func (tn *toolNotifier) Name() string       { return "acp_tool_notifier" }
func (tn *toolNotifier) Tools() []bond.Tool { return nil }

func (tn *toolNotifier) Init(r *bond.HookRegistry) {
	bond.OnBefore(r, bond.BeforeHookFunc[*bond.BeforeToolCallHook](tn.beforeToolCall))
	bond.OnAfter(r, bond.AfterHookFunc[*bond.AfterToolCallHook](tn.afterToolCall))
}

// beforeToolCall sends the "in_progress" notification when tool execution begins.
func (tn *toolNotifier) beforeToolCall(ctx context.Context, event *bond.BeforeToolCallHook) error {
	session := tn.session()
	if session == nil {
		return nil
	}

	notif := toolCallUpdateNotification{
		SessionID:  session.ID,
		Type:       agentacp.UpdateTypeToolCallUpdate,
		ToolCallID: event.ToolUse.ID,
		Status:     "in_progress",
	}

	return tn.sendNotification(notif)
}

// afterToolCall sends the "completed" or "failed" notification after tool execution.
func (tn *toolNotifier) afterToolCall(ctx context.Context, event *bond.AfterToolCallHook) {
	session := tn.session()
	if session == nil {
		return
	}

	notif := toolCallUpdateNotification{
		SessionID:  session.ID,
		Type:       agentacp.UpdateTypeToolCallUpdate,
		ToolCallID: event.ToolUse.ID,
	}

	if event.Result.IsError {
		notif.Status = "failed"
		notif.Error = extractTextContent(event.Result.Content)
	} else {
		notif.Status = "completed"
		notif.Result = extractTextContent(event.Result.Content)
	}

	tn.sendNotification(notif) //nolint:errcheck
}

// sendToolCallPending sends the "pending" notification when a tool use event is received.
func (tn *toolNotifier) sendToolCallPending(toolUse *bond.ToolUseBlock) error {
	session := tn.session()
	if session == nil {
		return nil
	}

	notif := toolCallNotification{
		SessionID:  session.ID,
		Type:       agentacp.UpdateTypeToolCall,
		ToolCallID: toolUse.ID,
		ToolName:   toolUse.Name,
		Status:     "pending",
	}

	return tn.sendNotification(notif)
}

// sendNotification marshals and sends a session/update notification.
func (tn *toolNotifier) sendNotification(params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}

	msg := Message{
		JSONRPC: "2.0",
		Method:  agentacp.MethodSessionUpdate,
		Params:  data,
	}

	msgData, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return tn.transport.WriteMessage(msgData)
}

// Verify interface compliance.
var _ bond.Plugin = (*toolNotifier)(nil)

// extractTextContent joins text blocks from a tool result into a single string.
func extractTextContent(blocks []bond.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		if tb, ok := b.(*bond.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}

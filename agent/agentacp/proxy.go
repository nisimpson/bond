package agentacp

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"github.com/nisimpson/bond"
)

// Requirement: 4.1 — THE ProxyAgent SHALL implement bond.Agent.
// Requirement: 4.2 — THE ProxyAgent SHALL extract the last user message text from the
// messages slice and submit it as a session/prompt request.
// Requirement: 4.3 — THE ProxyAgent SHALL translate agent_message_chunk notifications
// into StreamEventTextDelta events.
// Requirement: 4.4 — THE ProxyAgent SHALL translate tool_call notifications into
// StreamEventToolUse events with status "pending".
// Requirement: 4.5 — THE ProxyAgent SHALL translate tool_call_update notifications
// into StreamEventToolUse events with the updated status.
// Requirement: 4.6 — THE ProxyAgent SHALL map stop_reason from the prompt response to
// a bond.StopReason and yield a StreamEventStop.
// Requirement: 4.7 — THE ProxyAgent SHALL send a session/cancel notification when the
// context is cancelled.
// Requirement: 4.8 — THE ProxyAgent SHALL wait for the prompt response or CancelTimeout
// after sending session/cancel.
// Requirement: 5.1 — THE ProxyAgent SHALL handle session/request_permission requests by
// invoking the configured PermissionPolicy.
// Requirement: 5.3 — THE ProxyAgent SHALL respond with outcome "selected" when the policy
// returns Approve.
// Requirement: 5.4 — THE ProxyAgent SHALL respond with outcome "cancelled" when the policy
// returns Deny.
// Requirement: 6.1, 6.2, 6.3 — Stop reason mapping.
// Requirement: 7.1, 7.2, 7.3, 7.4 — Cancel behavior.

// ProxyAgent implements bond.Agent by delegating to the Client's ACP protocol.
// Each call to Stream sends a session/prompt and translates session/update
// notifications into bond.StreamEvent values.
type ProxyAgent struct {
	client *Client
}

// Compile-time check that ProxyAgent satisfies bond.Agent.
var _ bond.Agent = (*ProxyAgent)(nil)

// Stream sends the conversation messages to the external ACP agent and returns
// an iterator of streaming events. It extracts the last user message, sends it
// as a session/prompt, and translates incoming notifications into bond events.
func (p *ProxyAgent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		// 1. Extract prompt text from the last user message.
		prompt := extractLastUserMessage(messages)
		if prompt == "" {
			yield(bond.StreamEvent{}, fmt.Errorf("agentacp: no user message found"))
			return
		}

		// 2. Send prompt in background goroutine (dispatcher.Request blocks until response).
		type promptResult struct {
			resp *Message
			err  error
		}
		resultCh := make(chan promptResult, 1)
		go func() {
			resp, err := p.client.dispatcher.Request(ctx, MethodSessionPrompt, &sessionPromptRequest{
				SessionID: p.client.sessionID,
				Prompt:    []contentBlock{{Type: "text", Text: prompt}},
			})
			resultCh <- promptResult{resp: resp, err: err}
		}()

		// 3. Consume notifications until response arrives or context is cancelled.
		cancelTimeout := p.client.opts.CancelTimeout

		for {
			select {
			case notif := <-p.client.dispatcher.Notifications():
				// Handle permission requests (server-initiated request: has ID + method).
				if notif.Method == MethodRequestPermission && notif.ID != nil {
					if err := p.handlePermissionRequest(ctx, notif); err != nil {
						yield(bond.StreamEvent{}, fmt.Errorf("agentacp: permission response failed: %w", err))
						return
					}
					continue
				}
				// Translate notification to stream event.
				event, ok := translateNotification(notif)
				if ok {
					if !yield(event, nil) {
						return
					}
				}

			case result := <-resultCh:
				if result.err != nil {
					yield(bond.StreamEvent{}, result.err)
					return
				}
				// Drain any remaining notifications that arrived before the response.
			drain:
				for {
					select {
					case notif := <-p.client.dispatcher.Notifications():
						if notif.Method == MethodRequestPermission && notif.ID != nil {
							if err := p.handlePermissionRequest(ctx, notif); err != nil {
								yield(bond.StreamEvent{}, fmt.Errorf("agentacp: permission response failed: %w", err))
								return
							}
							continue
						}
						event, ok := translateNotification(notif)
						if ok {
							if !yield(event, nil) {
								return
							}
						}
					default:
						break drain
					}
				}
				// Map stop_reason to bond.StopReason and yield stop event.
				var promptResp sessionPromptResponse
				if err := json.Unmarshal(result.resp.Result, &promptResp); err != nil {
					yield(bond.StreamEvent{}, fmt.Errorf("agentacp: parse prompt response: %w", err))
					return
				}
				stopReason := mapStopReason(promptResp.StopReason)
				yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: stopReason}, nil)
				return

			case <-ctx.Done():
				// Send session/cancel notification to the external agent.
				_ = p.client.dispatcher.Notify(MethodSessionCancel, &struct {
					SessionID string `json:"sessionId"`
				}{SessionID: p.client.sessionID})
				// Wait for the prompt response or timeout.
				select {
				case result := <-resultCh:
					if result.err == nil {
						var promptResp sessionPromptResponse
						_ = json.Unmarshal(result.resp.Result, &promptResp)
						yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
					} else {
						yield(bond.StreamEvent{}, ctx.Err())
					}
				case <-time.After(cancelTimeout):
					yield(bond.StreamEvent{}, ErrCancelTimeout)
				}
				return
			}
		}
	}
}

// extractLastUserMessage finds the last message with role User and extracts the
// text from the first TextBlock in its content.
func extractLastUserMessage(messages []bond.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != bond.RoleUser {
			continue
		}
		for _, block := range messages[i].Content {
			if tb, ok := block.(*bond.TextBlock); ok && tb.Text != "" {
				return tb.Text
			}
		}
	}
	return ""
}

// sessionUpdateEnvelope is the outer params of a session/update notification.
type sessionUpdateEnvelope struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// sessionUpdateHeader is the common header inside the update field.
type sessionUpdateHeader struct {
	SessionUpdate string `json:"sessionUpdate"`
}

// agentMessageChunk is a session/update with sessionUpdate "agent_message_chunk".
type agentMessageChunk struct {
	SessionUpdate string        `json:"sessionUpdate"`
	MessageID     string        `json:"messageId"`
	Content       *contentBlock `json:"content,omitempty"`
}

// toolCallNotification is a session/update with sessionUpdate "tool_call".
type toolCallNotification struct {
	SessionUpdate string `json:"sessionUpdate"`
	ToolCallID    string `json:"toolCallId"`
	Title         string `json:"title,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status"`
}

// toolCallUpdateNotification is a session/update with sessionUpdate "tool_call_update".
type toolCallUpdateNotification struct {
	SessionUpdate string `json:"sessionUpdate"`
	ToolCallID    string `json:"toolCallId"`
	Status        string `json:"status"`
}

// permissionRequestParams is the params of a session/request_permission request.
type permissionRequestParams struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Input      json.RawMessage `json:"input"`
}

// permissionResponseResult is the client's response to a permission request.
type permissionResponseResult struct {
	Outcome string `json:"outcome"` // "selected" or "cancelled"
}

// translateNotification converts an ACP notification into a bond.StreamEvent.
// Returns false if the notification is not translatable (unknown type).
func translateNotification(msg *Message) (bond.StreamEvent, bool) {
	if msg.Method != MethodSessionUpdate {
		return bond.StreamEvent{}, false
	}

	// Parse the outer envelope to get the update field.
	var envelope sessionUpdateEnvelope
	if err := json.Unmarshal(msg.Params, &envelope); err != nil {
		return bond.StreamEvent{}, false
	}

	// Parse the update header to determine the type.
	var header sessionUpdateHeader
	if err := json.Unmarshal(envelope.Update, &header); err != nil {
		return bond.StreamEvent{}, false
	}

	switch header.SessionUpdate {
	case UpdateTypeAgentMessageChunk:
		var chunk agentMessageChunk
		if err := json.Unmarshal(envelope.Update, &chunk); err != nil {
			return bond.StreamEvent{}, false
		}
		text := ""
		if chunk.Content != nil {
			text = chunk.Content.Text
		}
		return bond.StreamEvent{
			Type:      bond.StreamEventTextDelta,
			TextDelta: text,
		}, true

	case UpdateTypeToolCall:
		var tc toolCallNotification
		if err := json.Unmarshal(envelope.Update, &tc); err != nil {
			return bond.StreamEvent{}, false
		}
		return bond.StreamEvent{
			Type: bond.StreamEventToolUse,
			ToolUse: &bond.ToolUseBlock{
				ID:   tc.ToolCallID,
				Name: tc.Title,
			},
			Metadata: map[string]any{"status": tc.Status},
		}, true

	case UpdateTypeToolCallUpdate:
		var tcu toolCallUpdateNotification
		if err := json.Unmarshal(envelope.Update, &tcu); err != nil {
			return bond.StreamEvent{}, false
		}
		return bond.StreamEvent{
			Type: bond.StreamEventToolUse,
			ToolUse: &bond.ToolUseBlock{
				ID: tcu.ToolCallID,
			},
			Metadata: map[string]any{"status": tcu.Status},
		}, true

	default:
		return bond.StreamEvent{}, false
	}
}

// handlePermissionRequest processes a session/request_permission request from the
// external agent. It invokes the configured PermissionPolicy and responds directly
// via the transport with outcome "selected" or "cancelled".
// Returns an error if responding to the external agent fails.
func (p *ProxyAgent) handlePermissionRequest(ctx context.Context, msg *Message) error {
	// Parse the permission request params.
	var params permissionRequestParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		// Can't parse — respond with cancelled to be safe.
		return p.respondToPermission(msg.ID, "cancelled")
	}

	// Determine which policy to use.
	policy := p.client.opts.PermissionPolicy
	if policy == nil {
		policy = tierPolicy(p.client.opts.PermissionTier)
	}

	// Build the permission request.
	req := PermissionRequest{
		ToolName:   params.ToolName,
		ToolCallID: params.ToolCallID,
		Input:      params.Input,
		Tier:       p.client.opts.PermissionTier,
	}

	// Invoke the policy.
	decision := policy(ctx, req)

	// Respond with appropriate outcome.
	outcome := "cancelled"
	if decision == Approve {
		outcome = "selected"
	}
	return p.respondToPermission(msg.ID, outcome)
}

// respondToPermission writes a JSON-RPC response to a permission request
// directly to the transport. Returns an error if the write fails.
func (p *ProxyAgent) respondToPermission(id *json.RawMessage, outcome string) error {
	result, _ := json.Marshal(&permissionResponseResult{Outcome: outcome})
	resp := &Message{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("agentacp: marshal permission response: %w", err)
	}
	return p.client.transport.WriteMessage(data)
}

// mapStopReason translates an ACP stop_reason string to a bond.StopReason.
func mapStopReason(reason string) bond.StopReason {
	switch reason {
	case "end_turn":
		return bond.StopReasonEnd
	case "max_tokens":
		return bond.StopReasonLength
	case "cancelled":
		return bond.StopReasonEnd
	default:
		return bond.StopReasonEnd
	}
}

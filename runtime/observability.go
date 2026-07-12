package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nisimpson/bond"
)

// Requirement 2.2, 2.10 — Structured observability notifications via ServerSession.Log.

// sessionLogFunc abstracts the ServerSession.Log call for testability.
type sessionLogFunc func(ctx context.Context, params *mcp.LoggingMessageParams) error

// observabilityPlugin emits structured log notifications via a ServerSession
// during agent execution. It registers hooks that fire notifications for
// hook lifecycle, tool call lifecycle, and state transitions.
type observabilityPlugin struct {
	logFn       sessionLogFunc
	streamStart time.Time
}

// Name implements bond.Plugin.
func (p *observabilityPlugin) Name() string { return "mcp_observability" }

// Tools implements bond.Plugin. The observability plugin contributes no tools.
func (p *observabilityPlugin) Tools() []bond.Tool { return nil }

// Init implements bond.Plugin. Registers observability hook handlers.
// Requirement 2.1, 2.3, 2.4, 2.5, 2.6, 2.7, 2.9
func (p *observabilityPlugin) Init(registry *bond.HookRegistry) {
	// BeforeStreamHook — record start time and emit hook_fired notification.
	// Requirement 2.3
	bond.OnBefore(registry, func(ctx context.Context, event *bond.BeforeStreamHook) error {
		p.streamStart = time.Now()
		p.notify(ctx, observabilityNotification{
			Type:  "hook_fired",
			Event: hookFiredEvent{Name: "BeforeStream"},
		})
		return nil
	})

	// AfterStreamHook — emit hook_completed notification with duration.
	// Requirement 2.4
	bond.OnAfter(registry, func(ctx context.Context, event *bond.AfterStreamHook) {
		duration := time.Since(p.streamStart).String()
		p.notify(ctx, observabilityNotification{
			Type:  "hook_completed",
			Event: hookCompletedEvent{Name: "AfterStream", Duration: duration},
		})
	})

	// BeforeModelInvokeHook — emit state_transition with state "thinking".
	// Requirement 2.7
	bond.OnBefore(registry, func(ctx context.Context, event *bond.BeforeModelInvokeHook) error {
		p.notify(ctx, observabilityNotification{
			Type:  "state_transition",
			Event: stateTransitionEvent{State: "thinking"},
		})
		return nil
	})

	// AfterModelInvokeHook — emit state_transition with state "generating_response" when stop reason is end.
	// Requirement 2.7
	bond.OnAfter(registry, func(ctx context.Context, event *bond.AfterModelInvokeHook) {
		if event.StopReason == bond.StopReasonEnd {
			p.notify(ctx, observabilityNotification{
				Type:  "state_transition",
				Event: stateTransitionEvent{State: "generating_response"},
			})
		}
	})

	// BeforeToolCycleHook — emit state_transition with state "executing_tools".
	// Requirement 2.7
	bond.OnBefore(registry, func(ctx context.Context, event *bond.BeforeToolCycleHook) error {
		p.notify(ctx, observabilityNotification{
			Type:  "state_transition",
			Event: stateTransitionEvent{State: "executing_tools"},
		})
		return nil
	})

	// BeforeToolCallHook — emit tool_invoked notification.
	// Requirement 2.5
	bond.OnBefore(registry, func(ctx context.Context, event *bond.BeforeToolCallHook) error {
		p.notify(ctx, observabilityNotification{
			Type:  "tool_invoked",
			Event: toolInvokedEvent{Name: event.ToolUse.Name},
		})
		return nil
	})

	// AfterToolCallHook — emit tool_result notification with success indicator.
	// Requirement 2.6
	bond.OnAfter(registry, func(ctx context.Context, event *bond.AfterToolCallHook) {
		p.notify(ctx, observabilityNotification{
			Type: "tool_result",
			Event: toolResultEvent{
				Name:    event.ToolUse.Name,
				Success: !event.Result.IsError,
			},
		})
	})
}

// Compile-time interface check.
var _ bond.Plugin = (*observabilityPlugin)(nil)

// ---------------------------------------------------------------------------
// Notification payload types
// ---------------------------------------------------------------------------

// observabilityNotification is the JSON payload sent via ServerSession.Log.
// Requirement 2.10 — structured JSON with type and event fields.
type observabilityNotification struct {
	Type  string `json:"type"`  // "hook_fired", "hook_completed", "tool_invoked", "tool_result", "state_transition"
	Event any    `json:"event"` // event-specific structured data
}

// hookFiredEvent is emitted when a hook begins execution.
// Requirement 2.3
type hookFiredEvent struct {
	Name string `json:"name"`
}

// hookCompletedEvent is emitted when a hook finishes execution.
// Requirement 2.4
type hookCompletedEvent struct {
	Name     string `json:"name"`
	Duration string `json:"duration"`
}

// toolInvokedEvent is emitted when a tool call begins.
// Requirement 2.5
type toolInvokedEvent struct {
	Name string `json:"name"`
}

// toolResultEvent is emitted when a tool call completes.
// Requirement 2.6
type toolResultEvent struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
}

// stateTransitionEvent is emitted when the agent changes state.
// Requirement 2.7
type stateTransitionEvent struct {
	State string `json:"state"`
}

// ---------------------------------------------------------------------------
// Helper method
// ---------------------------------------------------------------------------

// notify sends an observability notification via the log function.
// If the log call fails, the error is logged internally and execution continues.
// Requirement 2.9 — failures do not interrupt execution.
func (p *observabilityPlugin) notify(ctx context.Context, notif observabilityNotification) {
	err := p.logFn(ctx, &mcp.LoggingMessageParams{
		Level:  "info",
		Logger: "bond.agent",
		Data:   notif,
	})
	if err != nil {
		slog.Error("observability notification failed", "error", err, "type", notif.Type)
	}
}

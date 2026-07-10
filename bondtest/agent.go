// Package bondtest provides test utilities for the bond agent framework.
package bondtest

import (
	"context"
	"iter"

	"github.com/nisimpson/bond"
)

// Agent is a test agent that emits a preconfigured sequence of StreamEvents.
// Useful for deterministic testing of code that consumes bond.Agent.
//
// Example:
//
//	agent := &bondtest.Agent{
//	    Events: bondtest.TextEvents("Hello, ", "world!"),
//	}
//
//	resp, err := bond.Invoke(ctx, agent, bond.TextPrompt("hi"), bond.AgentOptions{})
//	// resp.Text == "Hello, world!"
type Agent struct {
	// Events is the sequence of events to emit on each Stream call.
	Events []bond.StreamEvent
	// Err, if set, is yielded as an error after all events are emitted.
	Err error
	// StreamFunc, if set, overrides Events/Err and provides full control.
	// Use for dynamic test behavior (e.g., inspecting input messages).
	StreamFunc func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error]
}

// Stream implements bond.Agent.
func (a *Agent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	if a.StreamFunc != nil {
		return a.StreamFunc(ctx, messages)
	}

	return func(yield func(bond.StreamEvent, error) bool) {
		for _, event := range a.Events {
			if !yield(event, nil) {
				return
			}
		}
		if a.Err != nil {
			yield(bond.StreamEvent{}, a.Err)
		}
	}
}

// TextEvents creates a sequence of StreamEvents that emit text deltas
// followed by a stop event. Simulates a simple text response.
func TextEvents(chunks ...string) []bond.StreamEvent {
	events := make([]bond.StreamEvent, 0, len(chunks)+2)
	events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})
	for _, chunk := range chunks {
		events = append(events, bond.StreamEvent{
			Type:      bond.StreamEventTextDelta,
			TextDelta: chunk,
		})
	}
	events = append(events, bond.StreamEvent{
		Type:       bond.StreamEventStop,
		StopReason: bond.StopReasonEnd,
	})
	return events
}

// ToolUseEvents creates a sequence of StreamEvents that emit a tool use
// request followed by a stop. Simulates the model requesting a tool call.
func ToolUseEvents(toolUse *bond.ToolUseBlock) []bond.StreamEvent {
	return []bond.StreamEvent{
		{Type: bond.StreamEventStart},
		{Type: bond.StreamEventToolUse, ToolUse: toolUse},
		{Type: bond.StreamEventStop, StopReason: bond.StopReasonToolUse},
	}
}

// Verify interface compliance.
var _ bond.Agent = (*Agent)(nil)

// Sequence creates a StreamFunc that returns different events on each call.
// First call returns events[0], second returns events[1], etc. Wraps around
// if more calls are made than event sets provided.
func Sequence(eventSets ...[]bond.StreamEvent) func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	callCount := 0
	return func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
		events := eventSets[callCount%len(eventSets)]
		callCount++
		return func(yield func(bond.StreamEvent, error) bool) {
			for _, event := range events {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
}

// Repeat creates a StreamFunc that always returns the same events on every call.
func Repeat(events []bond.StreamEvent) func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
		return func(yield func(bond.StreamEvent, error) bool) {
			for _, event := range events {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
}

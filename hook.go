package helix

import (
	"context"
	"errors"
	"reflect"
	"sync"
)

// ErrAbort is returned by a hook to signal that the current operation
// should be cancelled. Use with Before* hooks to prevent execution.
var ErrAbort = errors.New("helix: operation aborted by hook")

// HookEvent is the sealed interface for all hook event types.
type HookEvent interface {
	hookEvent() // sealed marker
}

// Hook handles a specific hook event.
type Hook interface {
	NotifyHookEvent(ctx context.Context, event HookEvent) error
}

// HookFunc adapts a typed function into a Hook. The function receives
// the concrete event type directly.
type HookFunc[T HookEvent] func(context.Context, T) error

func (fn HookFunc[T]) NotifyHookEvent(ctx context.Context, event HookEvent) error {
	e, ok := event.(T)
	if !ok {
		return nil // wrong type routed here; skip silently
	}
	return fn(ctx, e)
}

// HookRegistry manages hooks keyed by event type.
type HookRegistry struct {
	hooks sync.Map // map[reflect.Type][]Hook
}

// On registers a hook for the event type T. The type is inferred from the
// generic parameter — no event instance needed. Hooks fire in registration
// order (FIFO) when Notify is called for the corresponding event type.
func On[T HookEvent](r *HookRegistry, hook HookFunc[T]) {
	key := reflect.TypeFor[T]()
	for {
		existing, _ := r.hooks.Load(key)
		var list []Hook
		if existing != nil {
			list = existing.([]Hook)
		}
		updated := append(list, hook)
		if existing == nil {
			if _, loaded := r.hooks.LoadOrStore(key, updated); !loaded {
				return
			}
		} else {
			if r.hooks.CompareAndSwap(key, existing, updated) {
				return
			}
		}
	}
}

// Notify dispatches an event to all registered hooks for that event type.
// All hooks are called; errors are joined. If any hook returns ErrAbort,
// the joined error will contain it (check with errors.Is).
func (r *HookRegistry) Notify(ctx context.Context, event HookEvent) error {
	key := reflect.TypeOf(event)
	val, ok := r.hooks.Load(key)
	if !ok {
		return nil
	}

	var errs []error
	for _, hook := range val.([]Hook) {
		if err := hook.NotifyHookEvent(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ---------------------------------------------------------------------------
// Hook Events
// ---------------------------------------------------------------------------

// BeforeStreamHook fires before the agent loop begins.
type BeforeStreamHook struct {
	Messages []Message
}

func (*BeforeStreamHook) hookEvent() {}

// AfterStreamHook fires when the agent loop completes.
type AfterStreamHook struct {
	Messages []Message // full conversation history at completion
}

func (*AfterStreamHook) hookEvent() {}

// BeforeModelInvokeHook fires before calling the provider's Stream method.
type BeforeModelInvokeHook struct {
	Messages []Message // conversation state being sent to the model
}

func (*BeforeModelInvokeHook) hookEvent() {}

// AfterModelInvokeHook fires after a provider Stream call has fully drained.
type AfterModelInvokeHook struct {
	Blocks     []Block    // assembled assistant content blocks
	StopReason StopReason // why the model stopped
}

func (*AfterModelInvokeHook) hookEvent() {}

// OnStreamEventHook fires for each raw StreamEvent received from the provider.
type OnStreamEventHook struct {
	Event StreamEvent
}

func (*OnStreamEventHook) hookEvent() {}

// BeforeToolCallHook fires before a single tool is executed.
// Return ErrAbort to skip this tool call.
type BeforeToolCallHook struct {
	ToolUse *ToolUseBlock
}

func (*BeforeToolCallHook) hookEvent() {}

// AfterToolCallHook fires after a single tool execution completes.
type AfterToolCallHook struct {
	ToolUse *ToolUseBlock
	Result  *ToolResultBlock
}

func (*AfterToolCallHook) hookEvent() {}

// BeforeToolCycleHook fires before the batch of tool calls begins.
type BeforeToolCycleHook struct {
	ToolCalls []*ToolUseBlock
}

func (*BeforeToolCycleHook) hookEvent() {}

// AfterToolCycleHook fires after all tool calls in a cycle complete.
type AfterToolCycleHook struct {
	Results []*ToolResultBlock
}

func (*AfterToolCycleHook) hookEvent() {}

// OnMessageAppendedHook fires when a message is appended to conversation history.
type OnMessageAppendedHook struct {
	Message Message
}

func (*OnMessageAppendedHook) hookEvent() {}

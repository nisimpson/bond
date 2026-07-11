package bond

import (
	"context"
	"errors"
	"reflect"
	"sync"
)

// ErrAbort is returned by a before-hook to signal that the current operation
// should be cancelled. Use with Before* hooks to prevent execution.
var ErrAbort = errors.New("bond: operation aborted by hook")

// ---------------------------------------------------------------------------
// Hook Event Interfaces
// ---------------------------------------------------------------------------

// HookEvent is the sealed interface for all hook event types.
// Every hook event is either a [BeforeHookEvent] (gate) or [AfterHookEvent]
// (observer).
type HookEvent interface {
	hookEvent() // sealed marker
}

// BeforeHookEvent is implemented by gate hooks that fire before an operation.
// Gate hooks can return errors to prevent execution. Return [ErrAbort] to
// gracefully skip the operation, or any other error to halt with that error.
type BeforeHookEvent interface {
	HookEvent
	beforeHookEvent() // sealed marker
}

// AfterHookEvent is implemented by observer hooks that fire after an operation.
// Observer hooks are informational — their handlers cannot return errors and
// cannot interrupt the agent loop.
type AfterHookEvent interface {
	HookEvent
	afterHookEvent() // sealed marker
}

// ---------------------------------------------------------------------------
// Hook Handler Types
// ---------------------------------------------------------------------------

// Hook handles a specific hook event. This is the internal storage type.
type Hook interface {
	NotifyHookEvent(ctx context.Context, event HookEvent) error
}

// BeforeHookFunc is a handler for before (gate) events. It receives the
// concrete event type and may return an error to prevent the operation.
type BeforeHookFunc[T BeforeHookEvent] func(context.Context, T) error

// NotifyHookEvent implements [Hook].
func (fn BeforeHookFunc[T]) NotifyHookEvent(ctx context.Context, event HookEvent) error {
	e, ok := event.(T)
	if !ok {
		return nil // wrong type routed here; skip silently
	}
	return fn(ctx, e)
}

// AfterHookFunc is a handler for after (observer) events. It receives the
// concrete event type and cannot return an error — observer hooks are
// fire-and-forget.
type AfterHookFunc[T AfterHookEvent] func(context.Context, T)

// NotifyHookEvent implements [Hook]. Always returns nil since after-hooks
// cannot produce errors.
func (fn AfterHookFunc[T]) NotifyHookEvent(ctx context.Context, event HookEvent) error {
	e, ok := event.(T)
	if !ok {
		return nil
	}
	fn(ctx, e)
	return nil
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// HookRegistry manages hooks keyed by event type.
type HookRegistry struct {
	mu    sync.Mutex
	hooks map[reflect.Type][]Hook
}

// OnBefore registers a gate hook for the before-event type T. Gate hooks
// fire in registration order (FIFO). Return [ErrAbort] or any error to
// prevent the guarded operation.
func OnBefore[T BeforeHookEvent](r *HookRegistry, hook BeforeHookFunc[T]) {
	key := reflect.TypeFor[T]()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hooks == nil {
		r.hooks = make(map[reflect.Type][]Hook)
	}
	r.hooks[key] = append(r.hooks[key], hook)
}

// OnAfter registers an observer hook for the after-event type T. Observer
// hooks fire in registration order (FIFO). They cannot return errors and
// cannot interrupt the agent loop.
func OnAfter[T AfterHookEvent](r *HookRegistry, hook AfterHookFunc[T]) {
	key := reflect.TypeFor[T]()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hooks == nil {
		r.hooks = make(map[reflect.Type][]Hook)
	}
	r.hooks[key] = append(r.hooks[key], hook)
}

// Notify dispatches an event to all registered hooks for that event type.
// For before-events, all hooks are called and errors are joined. If any hook
// returns [ErrAbort], the joined error will contain it.
// For after-events, hooks are called but errors are always nil (since
// [AfterHookFunc] cannot produce errors).
func (r *HookRegistry) Notify(ctx context.Context, event HookEvent) error {
	key := reflect.TypeOf(event)
	r.mu.Lock()
	hooks := r.hooks[key]
	r.mu.Unlock()

	if len(hooks) == 0 {
		return nil
	}

	var errs []error
	for _, hook := range hooks {
		if err := hook.NotifyHookEvent(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ---------------------------------------------------------------------------
// Before Hook Events (gates — can prevent operations)
// ---------------------------------------------------------------------------

// BeforeStreamHook fires before the agent loop begins.
// Return [ErrAbort] to cancel the stream, or any error to stop with that error.
type BeforeStreamHook struct {
	Messages []Message
}

func (*BeforeStreamHook) hookEvent()       {}
func (*BeforeStreamHook) beforeHookEvent() {}

// BeforeModelInvokeHook fires before calling the provider's Stream method.
// Return [ErrAbort] to skip the invocation, or any error to stop the loop.
type BeforeModelInvokeHook struct {
	Messages []Message // conversation state being sent to the model
}

func (*BeforeModelInvokeHook) hookEvent()       {}
func (*BeforeModelInvokeHook) beforeHookEvent() {}

// BeforeStreamEventHook fires before each raw [StreamEvent] is processed.
// Return [ErrAbort] to stop consuming the stream.
type BeforeStreamEventHook struct {
	Event StreamEvent
}

func (*BeforeStreamEventHook) hookEvent()       {}
func (*BeforeStreamEventHook) beforeHookEvent() {}

// BeforeToolCycleHook fires before the batch of tool calls begins.
// Return [ErrAbort] to skip the entire tool cycle, or any error to abort
// all tool calls with error results.
type BeforeToolCycleHook struct {
	ToolCalls []*ToolUseBlock
}

func (*BeforeToolCycleHook) hookEvent()       {}
func (*BeforeToolCycleHook) beforeHookEvent() {}

// BeforeToolCallHook fires before a single tool is executed.
// Return [ErrAbort] or any error to skip this tool call and surface the
// error as a tool result to the model.
type BeforeToolCallHook struct {
	ToolUse *ToolUseBlock
}

func (*BeforeToolCallHook) hookEvent()       {}
func (*BeforeToolCallHook) beforeHookEvent() {}

// ---------------------------------------------------------------------------
// After Hook Events (observers — informational, cannot interrupt)
// ---------------------------------------------------------------------------

// AfterStreamHook fires when the agent loop completes.
type AfterStreamHook struct {
	Messages []Message // full conversation history at completion
}

func (*AfterStreamHook) hookEvent()      {}
func (*AfterStreamHook) afterHookEvent() {}

// AfterModelInvokeHook fires after a provider Stream call has fully drained.
type AfterModelInvokeHook struct {
	Blocks     []Block    // assembled assistant content blocks
	StopReason StopReason // why the model stopped
}

func (*AfterModelInvokeHook) hookEvent()      {}
func (*AfterModelInvokeHook) afterHookEvent() {}

// AfterToolCycleHook fires after all tool calls in a cycle complete.
type AfterToolCycleHook struct {
	Results []*ToolResultBlock
}

func (*AfterToolCycleHook) hookEvent()      {}
func (*AfterToolCycleHook) afterHookEvent() {}

// AfterToolCallHook fires after a single tool execution completes.
type AfterToolCallHook struct {
	ToolUse *ToolUseBlock
	Result  *ToolResultBlock
}

func (*AfterToolCallHook) hookEvent()      {}
func (*AfterToolCallHook) afterHookEvent() {}

// AfterMessageAppendedHook fires when a message is appended to conversation history.
type AfterMessageAppendedHook struct {
	Message Message
}

func (*AfterMessageAppendedHook) hookEvent()      {}
func (*AfterMessageAppendedHook) afterHookEvent() {}

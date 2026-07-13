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

// HookEvent is the interface for all hook event types.
// Every hook event is either a [BeforeHookEvent] (gate) or [AfterHookEvent]
// (observer). Users may define custom hook events by implementing these
// interfaces and firing them through a [HookRegistry].
type HookEvent interface {
	HookEvent() // marker method
}

// BeforeHookEvent is implemented by gate hooks that fire before an operation.
// Gate hooks can return errors to prevent execution. Return [ErrAbort] to
// gracefully skip the operation, or any other error to halt with that error.
type BeforeHookEvent interface {
	HookEvent
	BeforeHookEvent() // marker method
}

// AfterHookEvent is implemented by observer hooks that fire after an operation.
// Observer hooks are informational — their handlers cannot return errors and
// cannot interrupt the agent loop.
type AfterHookEvent interface {
	HookEvent
	AfterHookEvent() // marker method
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
	if r == nil {
		return nil
	}
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

// hookRegistryContextKey is the context key for the hook registry.
type hookRegistryContextKey struct{}

// WithHookRegistry attaches a [HookRegistry] to the context. Agent
// implementations (graphs, swarms) can retrieve it to fire custom events.
func WithHookRegistry(ctx context.Context, registry *HookRegistry) context.Context {
	return context.WithValue(ctx, hookRegistryContextKey{}, registry)
}

// HookRegistryFromContext retrieves the [HookRegistry] from a context.
// Returns nil if no registry is attached.
func HookRegistryFromContext(ctx context.Context) *HookRegistry {
	r, _ := ctx.Value(hookRegistryContextKey{}).(*HookRegistry)
	return r
}

// ---------------------------------------------------------------------------
// Before Hook Events (gates — can prevent operations)
// ---------------------------------------------------------------------------

// BeforeStreamHook fires before the agent loop begins.
// Return [ErrAbort] to cancel the stream, or any error to stop with that error.
type BeforeStreamHook struct {
	Messages []Message
}

func (*BeforeStreamHook) HookEvent()       {}
func (*BeforeStreamHook) BeforeHookEvent() {}

// BeforeModelInvokeHook fires before calling the provider's Stream method.
// Return [ErrAbort] to skip the invocation, or any error to stop the loop.
type BeforeModelInvokeHook struct {
	Messages []Message // conversation state being sent to the model
	Attempt  int       // 0 on first call, increments on retries
}

func (*BeforeModelInvokeHook) HookEvent()       {}
func (*BeforeModelInvokeHook) BeforeHookEvent() {}

// BeforeStreamEventHook fires before each raw [StreamEvent] is processed.
// Return [ErrAbort] to stop consuming the stream.
type BeforeStreamEventHook struct {
	Event StreamEvent
}

func (*BeforeStreamEventHook) HookEvent()       {}
func (*BeforeStreamEventHook) BeforeHookEvent() {}

// BeforeToolCycleHook fires before the batch of tool calls begins.
// Return [ErrAbort] to skip the entire tool cycle, or any error to abort
// all tool calls with error results.
type BeforeToolCycleHook struct {
	ToolCalls []*ToolUseBlock
}

func (*BeforeToolCycleHook) HookEvent()       {}
func (*BeforeToolCycleHook) BeforeHookEvent() {}

// BeforeToolCallHook fires before a single tool is executed.
// Return [ErrAbort] or any error to skip this tool call and surface the
// error as a tool result to the model.
type BeforeToolCallHook struct {
	ToolUse *ToolUseBlock
}

func (*BeforeToolCallHook) HookEvent()       {}
func (*BeforeToolCallHook) BeforeHookEvent() {}

// ---------------------------------------------------------------------------
// After Hook Events (observers — informational, cannot interrupt)
// ---------------------------------------------------------------------------

// AfterStreamHook fires when the agent loop completes.
type AfterStreamHook struct {
	Messages []Message // full conversation history at completion
}

func (*AfterStreamHook) HookEvent()      {}
func (*AfterStreamHook) AfterHookEvent() {}

// AfterModelInvokeHook fires after a provider Stream call has fully drained.
type AfterModelInvokeHook struct {
	Blocks     []Block    // assembled assistant content blocks
	StopReason StopReason // why the model stopped
}

func (*AfterModelInvokeHook) HookEvent()      {}
func (*AfterModelInvokeHook) AfterHookEvent() {}

// AfterToolCycleHook fires after all tool calls in a cycle complete.
type AfterToolCycleHook struct {
	Results []*ToolResultBlock
}

func (*AfterToolCycleHook) HookEvent()      {}
func (*AfterToolCycleHook) AfterHookEvent() {}

// AfterToolCallHook fires after a single tool execution completes.
type AfterToolCallHook struct {
	ToolUse *ToolUseBlock
	Result  *ToolResultBlock
}

func (*AfterToolCallHook) HookEvent()      {}
func (*AfterToolCallHook) AfterHookEvent() {}

// AfterMessageAppendedHook fires when a message is appended to conversation history.
type AfterMessageAppendedHook struct {
	Message Message
}

func (*AfterMessageAppendedHook) HookEvent()      {}
func (*AfterMessageAppendedHook) AfterHookEvent() {}

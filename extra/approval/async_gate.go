package approval

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/nisimpson/bond"
)

// AsyncGateOptions configures an [AsyncGate].
type AsyncGateOptions[T bond.BeforeHookEvent] struct {
	// Store is the backing Store for persisting approval records.
	Store Store
	// KeyFunc derives a deterministic store key from the hook event.
	// If nil, a UUID is generated for each new event (no deduplication).
	//
	// WARNING: A nil KeyFunc means the generated key is never returned to
	// the caller. This makes the async resume workflow unresolvable because
	// the caller cannot discover the key needed to call [Approve] or [Deny].
	// Use nil-KeyFunc only for one-shot deny/block semantics where no
	// subsequent approval is expected. For async suspend/resume workflows,
	// always provide a non-nil KeyFunc that produces a deterministic key
	// the caller can predict or retrieve.
	KeyFunc func(T) string
}

// AsyncGate implements [Gate]. It is an asynchronous approval gate that on
// first encounter persists a pending record and returns [ErrInterrupted]. On
// re-invocation it checks the store: approved → continue, denied →
// Result{Approved: false}, pending → [ErrInterrupted].
type AsyncGate[T bond.BeforeHookEvent] struct {
	opts AsyncGateOptions[T]
}

// NewAsyncGate creates an [AsyncGate] with the given options.
func NewAsyncGate[T bond.BeforeHookEvent](opts AsyncGateOptions[T]) *AsyncGate[T] {
	return &AsyncGate[T]{opts: opts}
}

// deriveKey returns the store key for the given event, using KeyFunc if
// configured or generating a new UUID otherwise.
func (g *AsyncGate[T]) deriveKey(event T) string {
	if g.opts.KeyFunc != nil {
		return g.opts.KeyFunc(event)
	}
	return uuid.New().String()
}

// RequestApproval checks the store for an existing approval record and returns
// the current decision. The semantics are:
//   - No record found: creates a pending record, returns Result{Approved: false}
//     with [ErrInterrupted].
//   - StatusApproved: returns Result{Approved: true}, nil.
//   - StatusDenied: returns Result{Approved: false, Reason: ...}, nil.
//   - StatusPending: returns Result{Approved: false} with [ErrInterrupted].
func (g *AsyncGate[T]) RequestApproval(ctx context.Context, event T) (Result, error) {
	key := g.deriveKey(event)

	record, err := g.opts.Store.Load(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("approval: load record %q: %w", key, err)
	}

	if record == nil {
		// First encounter — create pending record and interrupt.
		record = &Record{
			ID:        key,
			EventType: reflect.TypeFor[T]().String(),
			Status:    StatusPending,
			CreatedAt: time.Now(),
		}
		if err := g.opts.Store.Save(ctx, record); err != nil {
			return Result{}, fmt.Errorf("approval: save record %q: %w", key, err)
		}
		return Result{Approved: false}, ErrInterrupted
	}

	switch record.Status {
	case StatusApproved:
		return Result{Approved: true}, nil
	case StatusDenied:
		return Result{Approved: false, Reason: record.Reason}, nil
	default:
		// StatusPending — still waiting for external approval.
		return Result{Approved: false}, ErrInterrupted
	}
}

// Register adds the AsyncGate as a before-hook on the given registry.
// When the hook fires, it delegates to [AsyncGate.RequestApproval] and
// translates the result into hook semantics:
//   - Approved: returns nil (execution continues).
//   - Denied: returns [bond.ErrAbort].
//   - Pending/interrupted: returns [ErrInterrupted].
func (g *AsyncGate[T]) Register(registry *bond.HookRegistry) {
	bond.OnBefore(registry, func(ctx context.Context, event T) error {
		result, err := g.RequestApproval(ctx, event)
		if err != nil {
			return err // ErrInterrupted passes through
		}
		if !result.Approved {
			if result.Reason != "" {
				return fmt.Errorf("%w: %s", bond.ErrAbort, result.Reason)
			}
			return bond.ErrAbort
		}
		return nil
	})
}

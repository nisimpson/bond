package approval

import (
	"context"
	"fmt"

	"github.com/nisimpson/bond"
)

// Result is the decision from the approver.
type Result struct {
	Approved bool
	Reason   string // optional explanation for denial
}

// Gate decides whether an operation should proceed.
// Implementations can block (waiting for human input), call an external
// service, or evaluate a policy synchronously.
type Gate[T bond.BeforeHookEvent] interface {
	RequestApproval(ctx context.Context, event T) (Result, error)
}

// hookRequestGate is a function adapter that implements the Gate interface,
// allowing plain functions to be used as approval gates.
type hookRequestGate[T bond.BeforeHookEvent] func(context.Context, T) (Result, error)

// GateFunc wraps a plain function as a [Gate] implementation.
// This is a convenience for inline approval logic without defining a struct.
func GateFunc[T bond.BeforeHookEvent](fn func(context.Context, T) (Result, error)) Gate[T] {
	return hookRequestGate[T](fn)
}

// RequestApproval delegates to the underlying function to determine whether the
// operation should proceed.
func (fn hookRequestGate[T]) RequestApproval(ctx context.Context, req T) (Result, error) {
	return fn(ctx, req)
}

// Register adds the approval gate as a before-hook in the given registry.
// If the gate returns an error or denies approval, the hook aborts the
// operation by returning an error wrapping bond.ErrAbort.
func (fn hookRequestGate[T]) Register(registry *bond.HookRegistry) {
	bond.OnBefore(registry, func(ctx context.Context, event T) error {
		result, err := fn.RequestApproval(ctx, event)
		if err != nil {
			return fmt.Errorf("%w: %w", bond.ErrAbort, err)
		}
		if !result.Approved {
			return fmt.Errorf("%w: %s", bond.ErrAbort, result.Reason)
		}
		return nil
	})
}

// Register adds the given approval gate as a before-hook in the registry.
// If the gate denies the operation or returns an error, the hook aborts by
// returning an error wrapping bond.ErrAbort.
func Register[T bond.BeforeHookEvent](registry *bond.HookRegistry, gate Gate[T]) {
	hook := hookRequestGate[T](func(ctx context.Context, event T) (Result, error) {
		return gate.RequestApproval(ctx, event)
	})
	hook.Register(registry)
}

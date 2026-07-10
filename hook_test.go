package bond_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nisimpson/bond"
)

func TestHookRegistry_OnAndNotify(t *testing.T) {
	registry := &bond.HookRegistry{}
	var called bool

	bond.On[*bond.BeforeStreamHook](registry, func(ctx context.Context, e *bond.BeforeStreamHook) error {
		called = true
		if len(e.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(e.Messages))
		}
		return nil
	})

	err := registry.Notify(context.Background(), &bond.BeforeStreamHook{
		Messages: []bond.Message{{Role: bond.RoleUser}},
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !called {
		t.Error("hook was not called")
	}
}

func TestHookRegistry_FIFOOrder(t *testing.T) {
	registry := &bond.HookRegistry{}
	var order []int

	bond.On[*bond.AfterStreamHook](registry, func(ctx context.Context, e *bond.AfterStreamHook) error {
		order = append(order, 1)
		return nil
	})
	bond.On[*bond.AfterStreamHook](registry, func(ctx context.Context, e *bond.AfterStreamHook) error {
		order = append(order, 2)
		return nil
	})
	bond.On[*bond.AfterStreamHook](registry, func(ctx context.Context, e *bond.AfterStreamHook) error {
		order = append(order, 3)
		return nil
	})

	_ = registry.Notify(context.Background(), &bond.AfterStreamHook{})

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("expected [1,2,3], got %v", order)
	}
}

func TestHookRegistry_ErrorJoining(t *testing.T) {
	registry := &bond.HookRegistry{}

	bond.On[*bond.BeforeModelInvokeHook](registry, func(ctx context.Context, e *bond.BeforeModelInvokeHook) error {
		return errors.New("err1")
	})
	bond.On[*bond.BeforeModelInvokeHook](registry, func(ctx context.Context, e *bond.BeforeModelInvokeHook) error {
		return errors.New("err2")
	})

	err := registry.Notify(context.Background(), &bond.BeforeModelInvokeHook{})
	if err == nil {
		t.Fatal("expected error")
	}
	// Both errors should be present.
	if !errors.Is(err, err) {
		t.Error("errors not joined properly")
	}
}

func TestHookRegistry_NoHooks(t *testing.T) {
	registry := &bond.HookRegistry{}

	err := registry.Notify(context.Background(), &bond.BeforeStreamHook{})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestHookRegistry_WrongType(t *testing.T) {
	registry := &bond.HookRegistry{}
	var called bool

	bond.On[*bond.BeforeStreamHook](registry, func(ctx context.Context, e *bond.BeforeStreamHook) error {
		called = true
		return nil
	})

	// Notify with a different event type — should not call the hook.
	_ = registry.Notify(context.Background(), &bond.AfterStreamHook{})
	if called {
		t.Error("hook should not be called for wrong event type")
	}
}

func TestHookFunc_TypeMismatch(t *testing.T) {
	fn := bond.HookFunc[*bond.BeforeStreamHook](func(ctx context.Context, e *bond.BeforeStreamHook) error {
		return errors.New("should not reach")
	})

	// Call with wrong type — should return nil.
	err := fn.NotifyHookEvent(context.Background(), &bond.AfterStreamHook{})
	if err != nil {
		t.Errorf("expected nil for type mismatch, got %v", err)
	}
}

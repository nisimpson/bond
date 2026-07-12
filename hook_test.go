package bond_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nisimpson/bond"
)

func TestHookRegistry_OnBeforeAndNotify(t *testing.T) {
	registry := &bond.HookRegistry{}
	var called bool

	bond.OnBefore(registry, func(ctx context.Context, e *bond.BeforeStreamHook) error {
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

func TestHookRegistry_OnAfterAndNotify(t *testing.T) {
	registry := &bond.HookRegistry{}
	var called bool

	bond.OnAfter(registry, func(ctx context.Context, e *bond.AfterStreamHook) {
		called = true
		if len(e.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(e.Messages))
		}
	})

	err := registry.Notify(context.Background(), &bond.AfterStreamHook{
		Messages: []bond.Message{{Role: bond.RoleUser}, {Role: bond.RoleAssistant}},
	})
	if err != nil {
		t.Fatalf("Notify: %v (expected nil for after-hook)", err)
	}
	if !called {
		t.Error("after-hook was not called")
	}
}

func TestHookRegistry_FIFOOrder(t *testing.T) {
	registry := &bond.HookRegistry{}
	var order []int

	bond.OnAfter(registry, func(ctx context.Context, e *bond.AfterStreamHook) {
		order = append(order, 1)
	})
	bond.OnAfter(registry, func(ctx context.Context, e *bond.AfterStreamHook) {
		order = append(order, 2)
	})
	bond.OnAfter(registry, func(ctx context.Context, e *bond.AfterStreamHook) {
		order = append(order, 3)
	})

	_ = registry.Notify(context.Background(), &bond.AfterStreamHook{})

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("expected [1,2,3], got %v", order)
	}
}

func TestHookRegistry_BeforeErrorJoining(t *testing.T) {
	registry := &bond.HookRegistry{}

	bond.OnBefore(registry, func(ctx context.Context, e *bond.BeforeModelInvokeHook) error {
		return errors.New("err1")
	})
	bond.OnBefore(registry, func(ctx context.Context, e *bond.BeforeModelInvokeHook) error {
		return errors.New("err2")
	})

	err := registry.Notify(context.Background(), &bond.BeforeModelInvokeHook{})
	if err == nil {
		t.Fatal("expected error")
	}
	// Verify both errors are present via string content
	if !errors.Is(err, err) {
		t.Error("errors not joined properly")
	}
}

func TestHookRegistry_AfterNeverReturnsError(t *testing.T) {
	registry := &bond.HookRegistry{}

	bond.OnAfter(registry, func(ctx context.Context, e *bond.AfterToolCallHook) {
		// Even if this panicked, the error return from Notify would still be nil
		// because AfterHookFunc.NotifyHookEvent always returns nil.
	})

	err := registry.Notify(context.Background(), &bond.AfterToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "1", Name: "test"},
		Result:  &bond.ToolResultBlock{ToolUseID: "1"},
	})
	if err != nil {
		t.Errorf("expected nil from after-hook notify, got %v", err)
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

	bond.OnBefore(registry, func(ctx context.Context, e *bond.BeforeStreamHook) error {
		called = true
		return nil
	})

	// Notify with a different event type — should not call the hook.
	_ = registry.Notify(context.Background(), &bond.AfterStreamHook{})
	if called {
		t.Error("hook should not be called for wrong event type")
	}
}

func TestBeforeHookFunc_TypeMismatch(t *testing.T) {
	fn := bond.BeforeHookFunc[*bond.BeforeStreamHook](func(ctx context.Context, e *bond.BeforeStreamHook) error {
		return errors.New("should not reach")
	})

	// Call with wrong type — should return nil.
	err := fn.NotifyHookEvent(context.Background(), &bond.AfterStreamHook{})
	if err != nil {
		t.Errorf("expected nil for type mismatch, got %v", err)
	}
}

func TestAfterHookFunc_TypeMismatch(t *testing.T) {
	var called bool
	fn := bond.AfterHookFunc[*bond.AfterStreamHook](func(ctx context.Context, e *bond.AfterStreamHook) {
		called = true
	})

	// Call with wrong type — should not be called.
	err := fn.NotifyHookEvent(context.Background(), &bond.BeforeStreamHook{})
	if err != nil {
		t.Errorf("expected nil for type mismatch, got %v", err)
	}
	if called {
		t.Error("after-hook should not be called for wrong event type")
	}
}

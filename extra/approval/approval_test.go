package approval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/approval"
)

// mockGate is a test Gate implementation for BeforeToolCallHook.
type mockGate struct {
	result approval.Result
	err    error
	called bool
	event  *bond.BeforeToolCallHook
}

func (g *mockGate) RequestApproval(ctx context.Context, event *bond.BeforeToolCallHook) (approval.Result, error) {
	g.called = true
	g.event = event
	return g.result, g.err
}

func TestRegister_Approved(t *testing.T) {
	gate := &mockGate{result: approval.Result{Approved: true}}

	registry := &bond.HookRegistry{}
	approval.Register(registry, gate)

	event := &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "call-1", Name: "my_tool"},
	}

	err := registry.Notify(context.Background(), event)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !gate.called {
		t.Fatal("expected gate to be called")
	}
	if gate.event.ToolUse.Name != "my_tool" {
		t.Fatalf("expected tool name 'my_tool', got %q", gate.event.ToolUse.Name)
	}
}

func TestRegister_Denied(t *testing.T) {
	gate := &mockGate{result: approval.Result{Approved: false, Reason: "not allowed"}}

	registry := &bond.HookRegistry{}
	approval.Register(registry, gate)

	event := &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "call-2", Name: "dangerous_tool"},
	}

	err := registry.Notify(context.Background(), event)
	if err == nil {
		t.Fatal("expected error on denial")
	}
	if !errors.Is(err, bond.ErrAbort) {
		t.Fatalf("expected ErrAbort, got: %v", err)
	}
	if !gate.called {
		t.Fatal("expected gate to be called")
	}
}

func TestRegister_GateError(t *testing.T) {
	gate := &mockGate{err: errors.New("transport failure")}

	registry := &bond.HookRegistry{}
	approval.Register(registry, gate)

	event := &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "call-3", Name: "some_tool"},
	}

	err := registry.Notify(context.Background(), event)
	if err == nil {
		t.Fatal("expected error on gate failure")
	}
	if !errors.Is(err, bond.ErrAbort) {
		t.Fatalf("expected ErrAbort in chain, got: %v", err)
	}
}

func TestRegister_FunctionAdapter(t *testing.T) {
	var called bool

	gate := approval.GateFunc[*bond.BeforeModelInvokeHook](func(ctx context.Context, event *bond.BeforeModelInvokeHook) (approval.Result, error) {
		called = true
		if event.Attempt > 0 {
			return approval.Result{Approved: false, Reason: "no retries allowed"}, nil
		}
		return approval.Result{Approved: true}, nil
	})

	registry := &bond.HookRegistry{}
	approval.Register(registry, gate)

	// First attempt — approved.
	err := registry.Notify(context.Background(), &bond.BeforeModelInvokeHook{Attempt: 0})
	if err != nil {
		t.Fatalf("expected approval on attempt 0, got: %v", err)
	}
	if !called {
		t.Fatal("expected gate function to be called")
	}

	// Retry attempt — denied.
	err = registry.Notify(context.Background(), &bond.BeforeModelInvokeHook{Attempt: 1})
	if err == nil {
		t.Fatal("expected denial on attempt 1")
	}
	if !errors.Is(err, bond.ErrAbort) {
		t.Fatalf("expected ErrAbort, got: %v", err)
	}
}

func TestRegister_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	gate := &mockGate{result: approval.Result{Approved: true}}

	registry := &bond.HookRegistry{}
	approval.Register(registry, gate)

	event := &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "call-4", Name: "tool"},
	}

	// Gate still gets called (context handling is gate's responsibility).
	err := registry.Notify(ctx, event)
	if err != nil {
		t.Fatalf("expected no error (gate approves regardless), got: %v", err)
	}
}

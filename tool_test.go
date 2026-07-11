package bond_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nisimpson/bond"
)

func TestToolConfirmationFunc_Allowed(t *testing.T) {
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		return true, nil
	})

	allowed, err := provider.ConfirmToolUse(context.Background(), &bond.ToolUseBlock{
		ID:    "1",
		Name:  "shell",
		Input: json.RawMessage(`{"command":"ls"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true")
	}
}

func TestToolConfirmationFunc_Denied(t *testing.T) {
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		return false, nil
	})

	allowed, err := provider.ConfirmToolUse(context.Background(), &bond.ToolUseBlock{
		ID:    "1",
		Name:  "shell",
		Input: json.RawMessage(`{"command":"rm -rf /"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false")
	}
}

func TestWithToolConfirmation_AllowsToolCall(t *testing.T) {
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		return true, nil
	})

	plugin := bond.NewToolConfirmationPlugin(provider)

	if plugin.Name() != "tool_confirmation" {
		t.Errorf("expected plugin name 'tool_confirmation', got %q", plugin.Name())
	}

	// Initialize the hook registry
	registry := &bond.HookRegistry{}
	plugin.Init(registry)

	// Fire BeforeToolCallHook — should pass through
	err := registry.Notify(context.Background(), &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{
			ID:    "1",
			Name:  "file_read",
			Input: json.RawMessage(`{"path":"/tmp/test.txt"}`),
		},
	})
	if err != nil {
		t.Fatalf("expected no error for allowed tool, got: %v", err)
	}
}

func TestWithToolConfirmation_DeniesToolCall(t *testing.T) {
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		return false, nil
	})

	plugin := bond.NewToolConfirmationPlugin(provider)
	registry := &bond.HookRegistry{}
	plugin.Init(registry)

	err := registry.Notify(context.Background(), &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{
			ID:    "2",
			Name:  "shell",
			Input: json.RawMessage(`{"command":"rm -rf /"}`),
		},
	})
	if err == nil {
		t.Fatal("expected error for denied tool call")
	}
	if !errors.Is(err, bond.ErrAbort) {
		t.Errorf("expected ErrAbort, got: %v", err)
	}
}

func TestWithToolConfirmation_ProviderError(t *testing.T) {
	providerErr := errors.New("network timeout")
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		return false, providerErr
	})

	plugin := bond.NewToolConfirmationPlugin(provider)
	registry := &bond.HookRegistry{}
	plugin.Init(registry)

	err := registry.Notify(context.Background(), &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{
			ID:    "3",
			Name:  "http_fetch",
			Input: json.RawMessage(`{"url":"https://example.com"}`),
		},
	})
	if err == nil {
		t.Fatal("expected error when provider fails")
	}
	// Should NOT be ErrAbort — it's a provider failure, not a denial
	if errors.Is(err, bond.ErrAbort) {
		t.Error("provider error should not be ErrAbort")
	}
	// Should wrap the original error
	if !errors.Is(err, providerErr) {
		t.Errorf("expected wrapped provider error, got: %v", err)
	}
}

func TestWithToolConfirmation_SelectiveConfirmation(t *testing.T) {
	// Provider that only denies "shell" tool calls
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		if tu.Name == "shell" {
			return false, nil
		}
		return true, nil
	})

	plugin := bond.NewToolConfirmationPlugin(provider)
	registry := &bond.HookRegistry{}
	plugin.Init(registry)

	// file_read should be allowed
	err := registry.Notify(context.Background(), &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "1", Name: "file_read", Input: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("expected file_read to be allowed, got: %v", err)
	}

	// shell should be denied
	err = registry.Notify(context.Background(), &bond.BeforeToolCallHook{
		ToolUse: &bond.ToolUseBlock{ID: "2", Name: "shell", Input: json.RawMessage(`{}`)},
	})
	if !errors.Is(err, bond.ErrAbort) {
		t.Errorf("expected shell to be denied with ErrAbort, got: %v", err)
	}
}

func TestWithToolConfirmation_NoTools(t *testing.T) {
	// Plugin should expose no tools
	provider := bond.ToolConfirmationFunc(func(ctx context.Context, tu *bond.ToolUseBlock) (bool, error) {
		return true, nil
	})

	plugin := bond.NewToolConfirmationPlugin(provider)
	if tools := plugin.Tools(); len(tools) != 0 {
		t.Errorf("expected no tools, got %d", len(tools))
	}
}

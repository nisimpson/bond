package builtin

import (
	"testing"

	bond "github.com/nisimpson/bond"
)

// Validates: TBOX-6.2
func TestPlugin_AllToolsNoFilter(t *testing.T) {
	p, err := New(Options{
		Sandbox: &SandboxConfig{BaseDirectory: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := p.Tools()
	if len(tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(tools))
	}
}

// Validates: TBOX-6.3
func TestPlugin_FilterTools(t *testing.T) {
	p, err := New(Options{
		Include: []string{ToolHTTPFetch, ToolEnv},
	})
	// No sandbox needed because only http_fetch and env are included
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := p.Tools()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

// Validates: TBOX-6.4
func TestPlugin_ErrorNoMatchingTools(t *testing.T) {
	_, err := New(Options{
		Include: []string{"nonexistent_tool"},
	})
	if err == nil {
		t.Fatal("expected error for no matching tools")
	}
}

// Validates: TBOX-6.7
func TestPlugin_ErrorSandboxToolsWithoutConfig(t *testing.T) {
	_, err := New(Options{
		Include: []string{ToolShell},
		// No Sandbox provided
	})
	if err == nil {
		t.Fatal("expected error for shell tool without sandbox config")
	}
}

// Validates: TBOX-6.6
func TestPlugin_SandboxIgnoredForNonSandboxTools(t *testing.T) {
	// Sandbox provided but only http + env are included — should succeed
	p, err := New(Options{
		Include: []string{ToolHTTPFetch, ToolEnv},
		Sandbox: &SandboxConfig{BaseDirectory: "/tmp"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Tools()) != 2 {
		t.Errorf("expected 2 tools, got %d", len(p.Tools()))
	}
}

// Validates: TBOX-6.1
func TestPlugin_ImplementsBondPlugin(t *testing.T) {
	// Compile-time check
	var _ bond.Plugin = (*Plugin)(nil)

	// Runtime check
	p, err := New(Options{
		Sandbox: &SandboxConfig{BaseDirectory: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "toolbox" {
		t.Errorf("expected name 'toolbox', got %q", p.Name())
	}
}

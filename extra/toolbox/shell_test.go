package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	bond "github.com/nisimpson/bond"
)

func runShellTool(t *testing.T, cfg *SandboxConfig, input ShellInput) (ShellOutput, error) {
	t.Helper()
	tool := newShellTool(cfg)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	blocks, err := tool.Run(context.Background(), raw)
	if err != nil {
		return ShellOutput{}, err
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one block")
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	var output ShellOutput
	if err := json.Unmarshal([]byte(tb.Text), &output); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	return output, nil
}

// Validates: TBOX-1.1
func TestShellTool_HappyPath(t *testing.T) {
	output, err := runShellTool(t, nil, ShellInput{Command: "echo hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", output.ExitCode)
	}
	if !strings.Contains(output.Output, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", output.Output)
	}
	if output.Truncated {
		t.Error("expected truncated to be false")
	}
}

// Validates: TBOX-1.4
func TestShellTool_NonZeroExitCode(t *testing.T) {
	output, err := runShellTool(t, nil, ShellInput{Command: "sh -c 'echo fail; exit 2'"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.ExitCode != 2 {
		t.Errorf("expected exit code 2, got %d", output.ExitCode)
	}
	if !strings.Contains(output.Output, "fail") {
		t.Errorf("expected output to contain 'fail', got %q", output.Output)
	}
}

// Validates: TBOX-1.2
func TestShellTool_TimeoutTermination(t *testing.T) {
	timeout := 1
	_, err := runShellTool(t, nil, ShellInput{
		Command:        "sleep 10",
		TimeoutSeconds: &timeout,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

// Validates: TBOX-1.8
func TestShellTool_DenylistRejection(t *testing.T) {
	cfg := &SandboxConfig{
		CommandDenylist: []string{"rm", "shutdown"},
	}
	_, err := runShellTool(t, cfg, ShellInput{Command: "rm -rf /"})
	if err == nil {
		t.Fatal("expected permission denied error")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-1.3
func TestShellTool_AllowlistEnforcement(t *testing.T) {
	cfg := &SandboxConfig{
		CommandAllowlist: []string{"echo", "cat"},
	}

	// Allowed command succeeds.
	output, err := runShellTool(t, cfg, ShellInput{Command: "echo allowed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output.Output, "allowed") {
		t.Errorf("expected output to contain 'allowed', got %q", output.Output)
	}

	// Disallowed command is rejected.
	_, err = runShellTool(t, cfg, ShellInput{Command: "ls -la"})
	if err == nil {
		t.Fatal("expected permission denied error")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-1.3, TBOX-1.9
func TestShellTool_AllowlistNoDenylist(t *testing.T) {
	cfg := &SandboxConfig{
		CommandAllowlist: []string{"echo"},
	}

	// Only echo is permitted.
	_, err := runShellTool(t, cfg, ShellInput{Command: "echo ok"})
	if err != nil {
		t.Fatalf("unexpected error for allowed command: %v", err)
	}

	// Anything else is rejected.
	_, err = runShellTool(t, cfg, ShellInput{Command: "cat /dev/null"})
	if err == nil {
		t.Fatal("expected permission denied error for non-allowlisted command")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-1.9
func TestShellTool_NeitherListPermitsAll(t *testing.T) {
	cfg := &SandboxConfig{} // No allowlist, no denylist.

	output, err := runShellTool(t, cfg, ShellInput{Command: "echo free"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output.Output, "free") {
		t.Errorf("expected output to contain 'free', got %q", output.Output)
	}
}

// Validates: TBOX-1.7
func TestShellTool_WorkingDirectoryValidation(t *testing.T) {
	_, err := runShellTool(t, nil, ShellInput{
		Command:          "echo test",
		WorkingDirectory: "/nonexistent_dir_xyz_12345",
	})
	if err == nil {
		t.Fatal("expected error for invalid working directory")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got: %v", err)
	}
}

// Validates: TBOX-1.1
func TestShellTool_OutputTruncation(t *testing.T) {
	// Generate output slightly larger than 1 MB.
	// Use dd to write 1 MB + 1 byte worth of data.
	output, err := runShellTool(t, nil, ShellInput{
		Command: "dd if=/dev/zero bs=1048577 count=1 2>/dev/null | tr '\\0' 'A'",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !output.Truncated {
		t.Error("expected output to be truncated")
	}
	if len(output.Output) != maxOutputBytes {
		t.Errorf("expected output length %d, got %d", maxOutputBytes, len(output.Output))
	}
}

// Validates: TBOX-1.10
func TestShellTool_NegativeTimeoutRunsWithoutDeadline(t *testing.T) {
	cfg := &SandboxConfig{
		ShellTimeout: -1 * time.Second,
	}
	// This command completes quickly, but it should run without any deadline.
	output, err := runShellTool(t, cfg, ShellInput{Command: "echo no-deadline"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output.Output, "no-deadline") {
		t.Errorf("expected output to contain 'no-deadline', got %q", output.Output)
	}
}

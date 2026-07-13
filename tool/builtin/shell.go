package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	bond "github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

// maxOutputBytes is the maximum size of captured shell output (1 MB).
const maxOutputBytes = 1 << 20

// defaultShellTimeout is the fallback timeout when none is configured.
const defaultShellTimeout = 30 * time.Second

// newShellTool creates a shell command execution tool configured with the
// given sandbox restrictions.
func newShellTool(cfg *SandboxConfig) bond.Tool {
	t, _ := bond.NewFuncTool(
		func(ctx context.Context, input ShellInput) (ShellOutput, error) {
			return runShell(ctx, input, cfg)
		},
		bond.FuncToolOptions{
			Name:        ToolShell,
			Description: "Execute a shell command and return its output.",
			InputSchema: tool.SchemaFor[ShellInput](),
		},
	)
	return t
}

// runShell implements the shell tool execution logic.
func runShell(ctx context.Context, input ShellInput, cfg *SandboxConfig) (ShellOutput, error) {
	// Requirements: TBOX-1.1, TBOX-1.2, TBOX-1.3, TBOX-1.4, TBOX-1.5, TBOX-1.6, TBOX-1.7, TBOX-1.8, TBOX-1.9, TBOX-1.10

	// Requirement: TBOX-1.1 — validate command is non-empty
	if strings.TrimSpace(input.Command) == "" {
		return ShellOutput{}, fmt.Errorf("%w: command must not be empty", ErrValidation)
	}

	// Extract first whitespace-delimited token as the command binary.
	firstToken := strings.Fields(input.Command)[0]

	// Requirement: TBOX-1.3, TBOX-1.8, TBOX-1.9 — allowlist/denylist enforcement
	if cfg != nil {
		if err := checkCommandPermission(firstToken, cfg); err != nil {
			return ShellOutput{}, err
		}
	}

	// Requirement: TBOX-1.7 — validate working directory if specified
	if input.WorkingDirectory != "" {
		info, err := os.Stat(input.WorkingDirectory)
		if err != nil || !info.IsDir() {
			return ShellOutput{}, fmt.Errorf("%w: working directory %q is invalid or does not exist", ErrValidation, input.WorkingDirectory)
		}
	}

	// Requirement: TBOX-1.6, TBOX-1.10 — determine timeout
	timeout := resolveTimeout(input.TimeoutSeconds, cfg)

	// Requirement: TBOX-1.2 — create context with timeout
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else if timeout < 0 {
		// Negative means no timeout; use background context without deadline.
		ctx = context.Background()
	}

	// Create command.
	cmd := exec.CommandContext(ctx, "sh", "-c", input.Command)

	// Requirement: TBOX-1.5 — set working directory if provided
	if input.WorkingDirectory != "" {
		cmd.Dir = input.WorkingDirectory
	}

	// Requirement: TBOX-1.1 — run command and capture combined output
	output, err := cmd.CombinedOutput()

	// Requirement: TBOX-1.2 — check for timeout
	if ctx.Err() == context.DeadlineExceeded {
		truncated := truncateOutput(output)
		return ShellOutput{}, fmt.Errorf("%w: command timed out after %v; partial output: %s",
			ErrTimeout, timeout, truncated)
	}

	// Requirement: TBOX-1.1 — truncate output to 1 MB if needed
	outputStr, isTruncated := applyOutputLimit(output)

	// Requirement: TBOX-1.4 — extract exit code
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Non-exit errors (e.g., command not found).
			return ShellOutput{}, fmt.Errorf("command execution failed: %w", err)
		}
	}

	return ShellOutput{
		Output:    outputStr,
		ExitCode:  exitCode,
		Truncated: isTruncated,
	}, nil
}

// checkCommandPermission validates the command's first token against the
// configured denylist and allowlist.
func checkCommandPermission(token string, cfg *SandboxConfig) error {
	// Denylist is checked first and overrides the allowlist.
	if len(cfg.CommandDenylist) > 0 {
		for _, denied := range cfg.CommandDenylist {
			if token == denied {
				return fmt.Errorf("%w: command %q is denied", ErrPermissionDenied, token)
			}
		}
	}

	// Allowlist: if non-nil and non-empty, only listed commands are permitted.
	if len(cfg.CommandAllowlist) > 0 {
		for _, allowed := range cfg.CommandAllowlist {
			if token == allowed {
				return nil
			}
		}
		return fmt.Errorf("%w: command %q is not in the allowlist", ErrPermissionDenied, token)
	}

	// Neither list configured (or denylist didn't match and allowlist is empty) → permit.
	return nil
}

// resolveTimeout determines the effective timeout using the priority:
// input timeout_seconds > config ShellTimeout > 30s default.
// A negative resolved timeout means no timeout.
func resolveTimeout(inputSeconds *int, cfg *SandboxConfig) time.Duration {
	// Input timeout_seconds takes highest priority.
	if inputSeconds != nil {
		return time.Duration(*inputSeconds) * time.Second
	}

	// Config ShellTimeout is next.
	if cfg != nil && cfg.ShellTimeout != 0 {
		return cfg.ShellTimeout
	}

	// Default to 30 seconds.
	return defaultShellTimeout
}

// truncateOutput returns a string representation of output, truncated to maxOutputBytes.
func truncateOutput(output []byte) string {
	if len(output) > maxOutputBytes {
		return string(output[:maxOutputBytes])
	}
	return string(output)
}

// applyOutputLimit returns the output string and whether it was truncated.
func applyOutputLimit(output []byte) (string, bool) {
	if len(output) > maxOutputBytes {
		return string(output[:maxOutputBytes]), true
	}
	return string(output), false
}

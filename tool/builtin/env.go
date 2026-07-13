package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"

	bond "github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

// newEnvTool creates an environment variable access tool. If allowlist is
// non-nil and non-empty, only the listed variable names may be accessed.
func newEnvTool(allowlist []string) bond.Tool {
	t, _ := bond.NewFuncTool(
		func(ctx context.Context, input EnvInput) (EnvOutput, error) {
			return runEnv(input, allowlist)
		},
		bond.FuncToolOptions{
			Name:        ToolEnv,
			Description: "Read an environment variable value.",
			InputSchema: tool.SchemaFor[EnvInput](),
		},
	)
	return t
}

// runEnv implements the env tool execution logic.
func runEnv(input EnvInput, allowlist []string) (EnvOutput, error) {
	// Requirements: TBOX-5.1, TBOX-5.2, TBOX-5.3, TBOX-5.4, TBOX-5.5

	// Requirement: TBOX-5.5 — validate name is non-empty and not only whitespace
	if strings.TrimSpace(input.Name) == "" {
		return EnvOutput{}, fmt.Errorf("%w: non-empty variable name is required", ErrValidation)
	}

	// Requirement: TBOX-5.3 — if allowlist configured, check name before lookup
	if len(allowlist) > 0 {
		if !isInAllowlist(input.Name, allowlist) {
			return EnvOutput{}, fmt.Errorf("%w: variable %q is not in the allowlist", ErrPermissionDenied, input.Name)
		}
	}

	// Requirement: TBOX-5.1, TBOX-5.4 — look up variable via os.LookupEnv
	value, ok := os.LookupEnv(input.Name)

	// Requirement: TBOX-5.2 — if not set, return not-found error
	if !ok {
		return EnvOutput{}, fmt.Errorf("%w: variable %q is not set", ErrNotFound, input.Name)
	}

	// Requirement: TBOX-5.1 — return value including empty strings
	return EnvOutput{
		Name:  input.Name,
		Value: value,
	}, nil
}

// isInAllowlist checks if name is present in the allowlist using case-sensitive comparison.
func isInAllowlist(name string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if name == allowed {
			return true
		}
	}
	return false
}

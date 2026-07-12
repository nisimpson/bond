package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	bond "github.com/nisimpson/bond"
)

// runEnvTool is a test helper that creates an env tool with the given allowlist,
// marshals the input, and returns the parsed EnvOutput or error.
func runEnvTool(t *testing.T, allowlist []string, input EnvInput) (EnvOutput, error) {
	t.Helper()
	tool := newEnvTool(allowlist)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	blocks, err := tool.Run(context.Background(), raw)
	if err != nil {
		return EnvOutput{}, err
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one block")
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	var output EnvOutput
	if err := json.Unmarshal([]byte(tb.Text), &output); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	return output, nil
}

// Validates: TBOX-5.1
func TestEnvTool_ExistingVariable(t *testing.T) {
	t.Setenv("TEST_ENV_EXISTING", "hello-world")
	output, err := runEnvTool(t, nil, EnvInput{Name: "TEST_ENV_EXISTING"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "TEST_ENV_EXISTING" {
		t.Errorf("expected Name %q, got %q", "TEST_ENV_EXISTING", output.Name)
	}
	if output.Value != "hello-world" {
		t.Errorf("expected Value %q, got %q", "hello-world", output.Value)
	}
}

// Validates: TBOX-5.1
func TestEnvTool_EmptyValue(t *testing.T) {
	t.Setenv("TEST_ENV_EMPTY", "")
	output, err := runEnvTool(t, nil, EnvInput{Name: "TEST_ENV_EMPTY"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "TEST_ENV_EMPTY" {
		t.Errorf("expected Name %q, got %q", "TEST_ENV_EMPTY", output.Name)
	}
	if output.Value != "" {
		t.Errorf("expected empty Value, got %q", output.Value)
	}
}

// Validates: TBOX-5.2
func TestEnvTool_NotFoundError(t *testing.T) {
	// Do not set this variable — it should not exist.
	_, err := runEnvTool(t, nil, EnvInput{Name: "TEST_ENV_DEFINITELY_UNSET_XYZ_999"})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// Validates: TBOX-5.3
func TestEnvTool_AllowlistPermitsListedVariable(t *testing.T) {
	t.Setenv("TEST_ENV_ALLOWED", "secret-value")
	allowlist := []string{"TEST_ENV_ALLOWED", "OTHER_VAR"}
	output, err := runEnvTool(t, allowlist, EnvInput{Name: "TEST_ENV_ALLOWED"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Value != "secret-value" {
		t.Errorf("expected Value %q, got %q", "secret-value", output.Value)
	}
}

// Validates: TBOX-5.3
func TestEnvTool_AllowlistRejectsUnlistedVariable(t *testing.T) {
	t.Setenv("TEST_ENV_SECRET", "should-not-see")
	allowlist := []string{"SAFE_VAR", "OTHER_SAFE"}
	_, err := runEnvTool(t, allowlist, EnvInput{Name: "TEST_ENV_SECRET"})
	if err == nil {
		t.Fatal("expected permission denied error")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-5.3
func TestEnvTool_CaseSensitiveAllowlist(t *testing.T) {
	t.Setenv("home", "some-value")
	allowlist := []string{"HOME"}
	// Requesting "home" (lowercase) should be rejected because the allowlist has "HOME".
	_, err := runEnvTool(t, allowlist, EnvInput{Name: "home"})
	if err == nil {
		t.Fatal("expected permission denied error for case mismatch")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-5.5
func TestEnvTool_EmptyNameValidation(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"spaces only", "   "},
		{"tab only", "\t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runEnvTool(t, nil, EnvInput{Name: tc.input})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("expected ErrValidation, got: %v", err)
			}
		})
	}
}

// Validates: TBOX-5.4
func TestEnvTool_NoAllowlistPermitsAll(t *testing.T) {
	t.Setenv("TEST_ENV_ANY_VAR", "open-access")
	// nil allowlist means no restriction.
	output, err := runEnvTool(t, nil, EnvInput{Name: "TEST_ENV_ANY_VAR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Value != "open-access" {
		t.Errorf("expected Value %q, got %q", "open-access", output.Value)
	}
}

package builtin

import (
	"errors"
	"strings"
	"testing"
	"testing/quick"
)

// TestShellTool_Property_DenylistAlwaysRejects verifies that any command whose
// first token appears in the denylist is always rejected with ErrPermissionDenied.
//
// **Validates: Requirements TBOX-1.8**
func TestShellTool_Property_DenylistAlwaysRejects(t *testing.T) {
	f := func(command string) bool {
		// Skip inputs that have no valid first token.
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return true
		}
		token := fields[0]

		// Configure denylist to contain this token.
		cfg := &SandboxConfig{
			CommandDenylist: []string{token},
		}
		err := checkCommandPermission(token, cfg)
		return errors.Is(err, ErrPermissionDenied)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property: denylist always rejects matching tokens — failed: %v", err)
	}
}

// TestShellTool_Property_AllowlistRejectsNonMatching verifies that when an
// allowlist is configured, any first token NOT in the allowlist is rejected
// with ErrPermissionDenied.
//
// **Validates: Requirements TBOX-1.3**
func TestShellTool_Property_AllowlistRejectsNonMatching(t *testing.T) {
	f := func(command string) bool {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return true
		}
		token := fields[0]

		// Use a fixed allowlist that is unlikely to contain random tokens.
		cfg := &SandboxConfig{
			CommandAllowlist: []string{"__allowed_sentinel_cmd__"},
		}

		// The token should be rejected unless it happens to equal the sentinel.
		err := checkCommandPermission(token, cfg)
		if token == "__allowed_sentinel_cmd__" {
			return err == nil
		}
		return errors.Is(err, ErrPermissionDenied)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property: allowlist rejects non-matching tokens — failed: %v", err)
	}
}

// TestShellTool_Property_NoListPermitsAll verifies that when neither an
// allowlist nor a denylist is configured, all commands are permitted.
//
// **Validates: Requirements TBOX-1.9**
func TestShellTool_Property_NoListPermitsAll(t *testing.T) {
	f := func(command string) bool {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return true
		}
		token := fields[0]

		// Empty config: no allowlist, no denylist.
		cfg := &SandboxConfig{}
		err := checkCommandPermission(token, cfg)
		return err == nil
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property: no list permits all commands — failed: %v", err)
	}
}

// TestShellTool_Property_DenylistOverridesAllowlist verifies that the denylist
// takes precedence: a token present in both the denylist and allowlist is still rejected.
//
// **Validates: Requirements TBOX-1.3, TBOX-1.8**
func TestShellTool_Property_DenylistOverridesAllowlist(t *testing.T) {
	f := func(command string) bool {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return true
		}
		token := fields[0]

		// Token is in both lists — denylist should win.
		cfg := &SandboxConfig{
			CommandAllowlist: []string{token},
			CommandDenylist:  []string{token},
		}
		err := checkCommandPermission(token, cfg)
		return errors.Is(err, ErrPermissionDenied)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property: denylist overrides allowlist — failed: %v", err)
	}
}

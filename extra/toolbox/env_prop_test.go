package toolbox

import (
	"errors"
	"strings"
	"testing"
	"testing/quick"
)

// TestProperty_EnvAllowlistIsolation verifies that when an allowlist is
// configured, only allowlisted variable names can produce values. All other
// names are rejected with ErrPermissionDenied regardless of whether the
// variable is actually set in the environment.
//
// **Validates: Requirements TBOX-5.3**
func TestProperty_EnvAllowlistIsolation(t *testing.T) {
	// Set up a known allowlist.
	allowlist := []string{"ALLOWED_VAR_1", "ALLOWED_VAR_2", "ALLOWED_VAR_3"}

	// Set ALL allowlisted variables so the only protection is the allowlist itself.
	for _, v := range allowlist {
		t.Setenv(v, "allowed-value")
	}

	f := func(name string) bool {
		// Skip empty/whitespace-only names — those hit validation before allowlist.
		if strings.TrimSpace(name) == "" {
			return true
		}

		_, err := runEnv(EnvInput{Name: name}, allowlist)

		// Check if name is in the allowlist.
		inAllowlist := false
		for _, a := range allowlist {
			if name == a {
				inAllowlist = true
				break
			}
		}

		if inAllowlist {
			// Allowlisted names should either succeed or return not-found.
			return err == nil || errors.Is(err, ErrNotFound)
		}
		// NOT in allowlist → must be rejected with ErrPermissionDenied.
		return errors.Is(err, ErrPermissionDenied)
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Errorf("env allowlist isolation property failed: %v", err)
	}
}

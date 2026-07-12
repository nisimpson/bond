package acpio

import (
	"testing"
	"testing/quick"
	"time"
)

// TestProperty_IdempotentStdioProcessClose verifies that calling Close() N times
// (N ≥ 1) on a started StdioProcess never panics and always returns nil.
//
// Feature: acp-proxy, Property 9: Idempotent Close
//
// **Validates: Requirements 2.6, 11.5**
func TestProperty_IdempotentStdioProcessClose(t *testing.T) {
	f := func(n uint8) bool {
		calls := int(n%10) + 1 // 1..10

		proc := NewStdioProcess("cat", nil, StdioOptions{
			Timeout: 2 * time.Second,
		})
		if err := proc.Start(); err != nil {
			t.Logf("Start failed: %v", err)
			return false
		}

		// Call Close() N times; each call must succeed without panic.
		for i := 0; i < calls; i++ {
			if err := proc.Close(); err != nil {
				t.Logf("Close() call %d/%d returned error: %v", i+1, calls, err)
				return false
			}
		}

		// ExitCode should return a consistent value after close.
		exitCode := proc.ExitCode()
		for i := 0; i < calls; i++ {
			if got := proc.ExitCode(); got != exitCode {
				t.Logf("ExitCode inconsistent: first=%d, call %d=%d", exitCode, i+1, got)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 50}); err != nil {
		t.Errorf("Property: Idempotent Close — failed: %v", err)
	}
}

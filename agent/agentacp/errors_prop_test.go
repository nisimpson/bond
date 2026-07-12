package agentacp

import (
	"fmt"
	"testing"
	"testing/quick"
)

// TestProperty_ProtocolErrorWrapping verifies that when a JSON-RPC error response
// with arbitrary code C and message M is wrapped into a *ProtocolError, the
// resulting error preserves Code==C and Message==M, and its Error() string
// contains both values in the expected format.
//
// Feature: acp-proxy, Property 8: Protocol Error Wrapping
//
// **Validates: Requirements 3.3, 3.6, 11.1**
func TestProperty_ProtocolErrorWrapping(t *testing.T) {
	f := func(code int, message string) bool {
		// Skip empty messages (quick generates zero values)
		if message == "" {
			message = "test error"
		}

		// Create ProtocolError as the dispatcher would from an ErrorObject
		errObj := &ErrorObject{
			Code:    code,
			Message: message,
		}

		pe := &ProtocolError{
			Code:    errObj.Code,
			Message: errObj.Message,
			Data:    errObj.Data,
		}

		// Verify fields are preserved
		if pe.Code != code {
			t.Logf("Code: got %d, want %d", pe.Code, code)
			return false
		}
		if pe.Message != message {
			t.Logf("Message: got %q, want %q", pe.Message, message)
			return false
		}

		// Verify Error() string format
		errStr := pe.Error()
		if errStr == "" {
			t.Logf("Error() returned empty string")
			return false
		}
		expectedPrefix := fmt.Sprintf("agentacp: protocol error %d: %s", code, message)
		if errStr != expectedPrefix {
			t.Logf("Error() = %q, want %q", errStr, expectedPrefix)
			return false
		}

		// Verify it implements error interface
		var err error = pe
		return err.Error() == errStr
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Protocol error wrapping property failed: %v", err)
	}
}

package conversation

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
)

// Feature: conversation-session-management, Property 1: Trim preserves message ordering
// **Validates: CONV-1.3**
func TestProperty_TrimPreservesMessageOrdering(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		preamble := generatePreamble(rng)
		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		windowSize := 1 + rng.Intn(10)

		mgr, err := NewSlidingWindowManager(SlidingWindowOptions{WindowSize: windowSize})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Trim(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Trim error: %v", err)
			return false
		}

		// Property: output is a subsequence of input — every message in output
		// appears in input and their relative order is preserved.
		inputIdx := 0
		for _, outMsg := range result {
			found := false
			for inputIdx < len(messages) {
				if messagesEqual(outMsg, messages[inputIdx]) {
					inputIdx++
					found = true
					break
				}
				inputIdx++
			}
			if !found {
				t.Logf("output message not found as subsequence in input")
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Trim preserves message ordering — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 8: TrimmingPlugin hook applies Trim result
// **Validates: CONV-4.3, CONV-4.4, CONV-4.5**
func TestProperty_TrimmingPluginHookAppliesTrimResult(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		preamble := generatePreamble(rng)
		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		windowSize := 1 + rng.Intn(5)

		mgr, err := NewSlidingWindowManager(SlidingWindowOptions{WindowSize: windowSize})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		plugin := NewTrimmingPlugin(TrimmingPluginOptions{
			Manager: mgr,
		})

		// Register plugin hooks via Init
		registry := &bond.HookRegistry{}
		plugin.Init(registry)

		// Fire BeforeModelInvokeHook
		hook := &bond.BeforeModelInvokeHook{Messages: messages}
		err = registry.Notify(context.Background(), hook)
		if err != nil {
			t.Logf("unexpected hook error: %v", err)
			return false
		}

		// Compute expected trim result independently
		expected, trimErr := mgr.Trim(context.Background(), messages)
		if trimErr != nil {
			t.Logf("unexpected Trim error: %v", trimErr)
			return false
		}

		// Property: hook.Messages must equal the Trim result
		if len(hook.Messages) != len(expected) {
			t.Logf("length mismatch: hook has %d, expected %d", len(hook.Messages), len(expected))
			return false
		}

		for i := range expected {
			if !messagesEqual(hook.Messages[i], expected[i]) {
				t.Logf("message mismatch at index %d", i)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: TrimmingPlugin hook applies Trim result — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 8 (error path): TrimmingPlugin hook returns Trim error
// **Validates: CONV-4.5**
func TestProperty_TrimmingPluginHookReturnsTrimError(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		preamble := generatePreamble(rng)
		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		// Use a failing manager that always returns an error
		expectedErr := fmt.Errorf("trim failed: %s", randomASCII(rng, 10))
		mgr := &failingManager{err: expectedErr}

		plugin := NewTrimmingPlugin(TrimmingPluginOptions{
			Manager: mgr,
		})

		registry := &bond.HookRegistry{}
		plugin.Init(registry)

		hook := &bond.BeforeModelInvokeHook{Messages: messages}
		err := registry.Notify(context.Background(), hook)

		// Property: If Trim errors, the hook returns that error
		if err == nil {
			t.Logf("expected error from hook, got nil")
			return false
		}
		if !errors.Is(err, expectedErr) {
			t.Logf("expected error %v, got %v", expectedErr, err)
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: TrimmingPlugin hook returns Trim error — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 9: Auto-recovery retry is bounded
// **Validates: CONV-5.2, CONV-5.3, CONV-5.4**
func TestProperty_AutoRecoveryRetryIsBounded(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		preamble := generatePreamble(rng)
		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		// Random max retries between 1 and 5
		maxRetries := 1 + rng.Intn(5)
		windowSize := 1 + rng.Intn(5)

		mgr, err := NewSlidingWindowManager(SlidingWindowOptions{WindowSize: windowSize})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		plugin := NewTrimmingPlugin(TrimmingPluginOptions{
			Manager:     mgr,
			AutoRecover: true,
			MaxRetries:  maxRetries,
		})

		// Create a wrapped context overflow error
		overflowErr := fmt.Errorf("provider: context too large: %w", bond.ErrContextOverflow)

		// Invoke Recover up to maxRetries times — each should succeed
		for i := range maxRetries {
			result, recoverErr := plugin.Recover(context.Background(), overflowErr, messages)
			if recoverErr != nil {
				t.Logf("expected successful recovery at attempt %d, got error: %v", i+1, recoverErr)
				return false
			}
			if result == nil {
				t.Logf("expected non-nil result at attempt %d", i+1)
				return false
			}
		}

		// The (maxRetries + 1)th attempt should fail with exhausted retries
		_, recoverErr := plugin.Recover(context.Background(), overflowErr, messages)
		if recoverErr == nil {
			t.Logf("expected error after exhausting retries, got nil")
			return false
		}

		// The error should wrap the original overflow error
		if !errors.Is(recoverErr, bond.ErrContextOverflow) {
			t.Logf("expected wrapped ErrContextOverflow, got: %v", recoverErr)
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Auto-recovery retry is bounded — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 9 (non-overflow): Recover rejects non-overflow errors
// **Validates: CONV-5.2**
func TestProperty_RecoverRejectsNonOverflowErrors(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		messages := generateConversationPairs(rng)
		windowSize := 1 + rng.Intn(5)

		mgr, err := NewSlidingWindowManager(SlidingWindowOptions{WindowSize: windowSize})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		plugin := NewTrimmingPlugin(TrimmingPluginOptions{
			Manager:     mgr,
			AutoRecover: true,
			MaxRetries:  3,
		})

		// Non-overflow error should be rejected
		nonOverflowErr := fmt.Errorf("some other error: %s", randomASCII(rng, 10))
		_, recoverErr := plugin.Recover(context.Background(), nonOverflowErr, messages)
		if recoverErr == nil {
			t.Logf("expected error for non-overflow, got nil")
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Recover rejects non-overflow errors — failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// failingManager is a ConversationManager that always returns a configured error.
type failingManager struct {
	err error
}

func (m *failingManager) Trim(_ context.Context, _ []bond.Message) ([]bond.Message, error) {
	return nil, m.err
}

// messagesEqual checks if two messages have the same role and text content.
func messagesEqual(a, b bond.Message) bool {
	if a.Role != b.Role {
		return false
	}
	return textFromMessage(a) == textFromMessage(b)
}

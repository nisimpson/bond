package conversation

import (
	"context"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
)

// generatePreamble creates 0-3 assistant messages to serve as a system preamble.
func generatePreamble(rng *rand.Rand) []bond.Message {
	count := rng.Intn(4) // 0..3
	msgs := make([]bond.Message, count)
	for i := range msgs {
		msgs[i] = bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 1+rng.Intn(20))}},
		}
	}
	return msgs
}

// generateConversationPairs creates 1-10 user/assistant pairs.
func generateConversationPairs(rng *rand.Rand) []bond.Message {
	pairs := 1 + rng.Intn(10)
	msgs := make([]bond.Message, 0, pairs*2)
	for range pairs {
		msgs = append(msgs, bond.Message{
			Role:    bond.RoleUser,
			Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 1+rng.Intn(20))}},
		})
		// Each user message followed by 1-2 assistant messages
		assistantCount := 1 + rng.Intn(2)
		for range assistantCount {
			msgs = append(msgs, bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 1+rng.Intn(20))}},
			})
		}
	}
	return msgs
}

// randomASCII generates a random printable ASCII string of the given length.
func randomASCII(rng *rand.Rand, length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = byte(32 + rng.Intn(95)) // printable ASCII range
	}
	return string(b)
}

// countUserMessages counts the number of user messages in a slice (excluding preamble).
func countUserMessages(messages []bond.Message) int {
	count := 0
	for _, m := range messages {
		if m.Role == bond.RoleUser {
			count++
		}
	}
	return count
}

// findPreambleEnd returns the index of the first user message (preamble boundary).
func findPreambleEnd(messages []bond.Message) int {
	for i, m := range messages {
		if m.Role == bond.RoleUser {
			return i
		}
	}
	return len(messages)
}

// Feature: conversation-session-management, Property 2: SlidingWindowManager bounds output to N pairs
// Validates: CONV-2.2, CONV-2.4
func TestProperty_SlidingWindowBoundsOutputToNPairs(t *testing.T) {
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

		// Count user messages in result after the preamble
		preambleEnd := findPreambleEnd(result)
		userCount := countUserMessages(result[preambleEnd:])

		// Property: user message count (pairs) must be at most N
		if userCount > windowSize {
			t.Logf("user count %d exceeds window size %d", userCount, windowSize)
			return false
		}

		// Property: the retained pairs are the N most recent from input
		// Find user messages in original conversation portion
		inputConv := messages[findPreambleEnd(messages):]
		var inputUserIndices []int
		for i, m := range inputConv {
			if m.Role == bond.RoleUser {
				inputUserIndices = append(inputUserIndices, i)
			}
		}

		// Find user messages in result conversation portion
		resultConv := result[preambleEnd:]
		var resultUserIndices []int
		for i, m := range resultConv {
			if m.Role == bond.RoleUser {
				resultUserIndices = append(resultUserIndices, i)
			}
		}

		// If trimming occurred, verify retained users are the last N from input
		if len(inputUserIndices) > windowSize && len(resultUserIndices) > 0 {
			// The first user message in the result should correspond to
			// the (total - windowSize)th user message in input
			expectedStartIdx := inputUserIndices[len(inputUserIndices)-windowSize]
			firstResultUserText := textFromMessage(resultConv[resultUserIndices[0]])
			expectedText := textFromMessage(inputConv[expectedStartIdx])
			if firstResultUserText != expectedText {
				t.Logf("first retained user message mismatch: got %q, want %q", firstResultUserText, expectedText)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: SlidingWindowManager bounds output to N pairs — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 3: SlidingWindowManager preserves system preamble
// Validates: CONV-2.3
func TestProperty_SlidingWindowPreservesSystemPreamble(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate at least 1 preamble message
		preambleCount := 1 + rng.Intn(3) // 1..3
		preamble := make([]bond.Message, preambleCount)
		for i := range preamble {
			preamble[i] = bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 1+rng.Intn(20))}},
			}
		}

		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		// Use a small window to force trimming
		windowSize := 1 + rng.Intn(3)

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

		// Property: the preamble messages appear at the start of output unchanged
		if len(result) < preambleCount {
			t.Logf("result length %d is less than preamble count %d", len(result), preambleCount)
			return false
		}

		for i := range preambleCount {
			resultText := textFromMessage(result[i])
			expectedText := textFromMessage(preamble[i])
			if resultText != expectedText {
				t.Logf("preamble[%d] mismatch: got %q, want %q", i, resultText, expectedText)
				return false
			}
			if result[i].Role != preamble[i].Role {
				t.Logf("preamble[%d] role mismatch: got %v, want %v", i, result[i].Role, preamble[i].Role)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: SlidingWindowManager preserves system preamble — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 4: Invalid window size rejects at construction
// Validates: CONV-2.5
func TestProperty_InvalidWindowSizeRejectsAtConstruction(t *testing.T) {
	f := func(n int) bool {
		// Only test non-positive values
		if n > 0 {
			n = -n
		}
		// n is now <= 0

		_, err := NewSlidingWindowManager(SlidingWindowOptions{WindowSize: n})
		if err == nil {
			t.Logf("expected error for window size %d, got nil", n)
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Invalid window size rejects at construction — failed: %v", err)
	}
}

// textFromMessage extracts text content from the first TextBlock in a message.
func textFromMessage(msg bond.Message) string {
	for _, b := range msg.Content {
		if tb, ok := b.(*bond.TextBlock); ok {
			return tb.Text
		}
	}
	return ""
}

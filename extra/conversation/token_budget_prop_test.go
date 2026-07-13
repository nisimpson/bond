package conversation

import (
	"context"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
)

// charCounter counts characters in all TextBlock content across messages.
func charCounter(messages []bond.Message) int {
	total := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			if tb, ok := block.(*bond.TextBlock); ok {
				total += len(tb.Text)
			}
		}
	}
	return total
}

// Feature: conversation-session-management, Property 5: TokenBudgetManager output fits within budget
// Validates: CONV-3.2
func TestProperty_TokenBudgetOutputFitsWithinBudget(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		preamble := generatePreamble(rng)
		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		// Calculate anchor cost (preamble + last user message)
		lastUserIdx := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == bond.RoleUser {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx == -1 {
			// No user messages; Trim returns unchanged, skip this case
			return true
		}

		anchors := make([]bond.Message, 0, len(preamble)+1)
		anchors = append(anchors, preamble...)
		anchors = append(anchors, messages[lastUserIdx])
		anchorCost := charCounter(anchors)

		// Pick a budget that is at least enough for the anchors plus some extra
		budget := anchorCost + 1 + rng.Intn(100)

		mgr, err := NewTokenBudgetManager(TokenBudgetOptions{
			MaxTokens: budget,
			Counter:   charCounter,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Trim(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Trim error: %v", err)
			return false
		}

		// Property: token count of output must not exceed budget
		resultTokens := charCounter(result)
		if resultTokens > budget {
			t.Logf("result tokens %d exceed budget %d", resultTokens, budget)
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: TokenBudgetManager output fits within budget — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 6: TokenBudgetManager preserves preamble and last user message
// Validates: CONV-3.3, CONV-3.4
func TestProperty_TokenBudgetPreservesPreambleAndLastUserMessage(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate at least 1 preamble message
		preambleCount := 1 + rng.Intn(3)
		preamble := make([]bond.Message, preambleCount)
		for i := range preamble {
			preamble[i] = bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 1+rng.Intn(20))}},
			}
		}

		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		// Find the last user message
		lastUserIdx := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == bond.RoleUser {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx == -1 {
			return true
		}

		// Calculate anchor cost and set budget large enough for anchors
		anchors := make([]bond.Message, 0, len(preamble)+1)
		anchors = append(anchors, preamble...)
		anchors = append(anchors, messages[lastUserIdx])
		anchorCost := charCounter(anchors)

		// Budget is large enough for anchors plus some room
		budget := anchorCost + 1 + rng.Intn(50)

		mgr, err := NewTokenBudgetManager(TokenBudgetOptions{
			MaxTokens: budget,
			Counter:   charCounter,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Trim(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Trim error: %v", err)
			return false
		}

		// Property: preamble appears at the start of the result
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
		}

		// Property: last user message appears at the end of the result
		lastResult := result[len(result)-1]
		expectedLastUser := messages[lastUserIdx]
		if lastResult.Role != bond.RoleUser {
			t.Logf("last result message role is %v, want user", lastResult.Role)
			return false
		}
		if textFromMessage(lastResult) != textFromMessage(expectedLastUser) {
			t.Logf("last user message mismatch: got %q, want %q",
				textFromMessage(lastResult), textFromMessage(expectedLastUser))
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: TokenBudgetManager preserves preamble and last user message — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 7: Insufficient budget returns error
// Validates: CONV-3.6
func TestProperty_TokenBudgetInsufficientBudgetReturnsError(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate at least 1 preamble message
		preambleCount := 1 + rng.Intn(3)
		preamble := make([]bond.Message, preambleCount)
		for i := range preamble {
			preamble[i] = bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 5+rng.Intn(20))}},
			}
		}

		conversation := generateConversationPairs(rng)
		messages := append(preamble, conversation...)

		// Find the last user message
		lastUserIdx := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == bond.RoleUser {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx == -1 {
			return true
		}

		// Calculate anchor cost
		anchors := make([]bond.Message, 0, len(preamble)+1)
		anchors = append(anchors, preamble...)
		anchors = append(anchors, messages[lastUserIdx])
		anchorCost := charCounter(anchors)

		// Set budget strictly less than anchor cost
		if anchorCost <= 1 {
			// Edge case: anchor cost too small to create insufficient budget
			return true
		}
		budget := 1 + rng.Intn(anchorCost-1) // budget in [1, anchorCost-1]

		mgr, err := NewTokenBudgetManager(TokenBudgetOptions{
			MaxTokens: budget,
			Counter:   charCounter,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		_, err = mgr.Trim(context.Background(), messages)

		// Property: Trim must return a non-nil error
		if err == nil {
			t.Logf("expected error for budget %d < anchor cost %d, got nil", budget, anchorCost)
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: Insufficient budget returns error — failed: %v", err)
	}
}

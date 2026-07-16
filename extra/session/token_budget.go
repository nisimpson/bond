package session

import (
	"context"
	"fmt"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
)

// TokenCounter estimates the token count for a message slice.
type TokenCounter func(messages []bond.Message) int

// TokenBudgetOptions configures a [TokenBudgetManager].
type TokenBudgetOptions struct {
	// MaxTokens is the maximum token budget. Must be positive.
	MaxTokens int
	// Counter estimates token usage for a slice of messages.
	Counter TokenCounter
}

// Requirement: CONV-3.1, CONV-3.5 — token budget construction and validation

// TokenBudgetManager trims messages to fit within a token budget,
// always preserving the system preamble and the most recent user message.
type TokenBudgetManager struct {
	maxTokens int
	counter   TokenCounter
}

// compile-time interface compliance check
var _ agent.HistoryPolicy = (*TokenBudgetManager)(nil)

// NewTokenBudgetManager creates a [TokenBudgetManager].
// Returns an error if opts.MaxTokens <= 0 or opts.Counter is nil.
func NewTokenBudgetManager(opts TokenBudgetOptions) (*TokenBudgetManager, error) {
	if opts.MaxTokens <= 0 {
		return nil, fmt.Errorf("session: max tokens must be positive, got %d", opts.MaxTokens)
	}
	if opts.Counter == nil {
		return nil, fmt.Errorf("session: token counter must not be nil")
	}
	return &TokenBudgetManager{
		maxTokens: opts.MaxTokens,
		counter:   opts.Counter,
	}, nil
}

// Requirement: CONV-3.2, CONV-3.3, CONV-3.4, CONV-3.6 — token budget select logic

// Select returns a subset of messages that fits within the configured token budget.
// The system preamble (messages before the first user message) and the most recent
// user message are always preserved. If these anchors alone exceed the budget, an
// error is returned. Otherwise, messages between the preamble and the last user
// message are included from newest to oldest until the budget is exhausted.
func (m *TokenBudgetManager) Select(_ context.Context, messages []bond.Message) ([]bond.Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// Find the last user message index.
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == bond.RoleUser {
			lastUserIdx = i
			break
		}
	}

	// If there are no user messages, return unchanged.
	if lastUserIdx == -1 {
		return messages, nil
	}

	// Identify the preamble: all messages before the first RoleUser message.
	preambleEnd := 0
	for i, msg := range messages {
		if msg.Role == bond.RoleUser {
			preambleEnd = i
			break
		}
	}

	preamble := messages[:preambleEnd]
	lastUserMsg := messages[lastUserIdx]

	// Estimate tokens for anchors (preamble + last user message).
	anchors := make([]bond.Message, 0, len(preamble)+1)
	anchors = append(anchors, preamble...)
	anchors = append(anchors, lastUserMsg)

	anchorTokens := m.counter(anchors)
	if anchorTokens > m.maxTokens {
		return nil, fmt.Errorf("session: anchors alone require %d tokens, exceeding budget of %d", anchorTokens, m.maxTokens)
	}

	// The middle portion is everything between preamble end and the last user message.
	middle := messages[preambleEnd:lastUserIdx]

	// Walk backwards through the middle, adding messages while within budget.
	// We build a candidate result and check the token count after each addition.
	selected := make([]bond.Message, 0, len(middle))
	for i := len(middle) - 1; i >= 0; i-- {
		candidate := make([]bond.Message, 0, len(preamble)+len(selected)+2)
		candidate = append(candidate, preamble...)
		candidate = append(candidate, middle[i])
		candidate = append(candidate, selected...)
		candidate = append(candidate, lastUserMsg)

		if m.counter(candidate) > m.maxTokens {
			break
		}
		// Prepend the message to selected (it fits).
		selected = append([]bond.Message{middle[i]}, selected...)
	}

	// Build final result: preamble + selected middle + last user message.
	result := make([]bond.Message, 0, len(preamble)+len(selected)+1)
	result = append(result, preamble...)
	result = append(result, selected...)
	result = append(result, lastUserMsg)

	return result, nil
}

package session

import (
	"context"
	"fmt"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
)

// SummarizationManagerOptions configures a [SummarizationManager].
type SummarizationManagerOptions struct {
	// Summarizer produces summaries of dropped messages.
	Summarizer Summarizer
	// Fallback is the underlying policy that determines what to drop.
	Fallback agent.HistoryPolicy
}

// SummarizationManager wraps a fallback [agent.HistoryPolicy], summarizing
// dropped messages via an LLM instead of discarding them. When the fallback
// policy trims messages from the conversation, this manager invokes the
// [Summarizer] to condense the dropped portion into a single message that
// is prepended to the retained history.
//
// The dropped-message computation assumes the fallback policy retains a
// contiguous suffix of the input (as [SlidingWindowManager] and
// [TokenBudgetManager] do). Custom fallback policies that retain
// non-contiguous subsets may produce incorrect summaries.
type SummarizationManager struct {
	summarizer Summarizer
	fallback   agent.HistoryPolicy
}

// compile-time interface compliance check
var _ agent.HistoryPolicy = (*SummarizationManager)(nil)

// NewSummarizationManager creates a [SummarizationManager].
// Returns an error if opts.Summarizer or opts.Fallback is nil.
func NewSummarizationManager(opts SummarizationManagerOptions) (*SummarizationManager, error) {
	if opts.Summarizer == nil {
		return nil, fmt.Errorf("session: summarizer must not be nil")
	}
	if opts.Fallback == nil {
		return nil, fmt.Errorf("session: fallback policy must not be nil")
	}
	return &SummarizationManager{
		summarizer: opts.Summarizer,
		fallback:   opts.Fallback,
	}, nil
}

// Select implements [agent.HistoryPolicy]. It identifies messages dropped by
// the fallback policy, summarizes them via the configured [Summarizer], and
// returns the system preamble followed by the summary message and the retained
// conversation messages. If the Summarizer returns an error, Select gracefully
// degrades by returning the fallback result without a summary.
func (m *SummarizationManager) Select(ctx context.Context, messages []bond.Message) ([]bond.Message, error) {
	// Step 1: Apply fallback policy.
	retained, err := m.fallback.Select(ctx, messages)
	if err != nil {
		return nil, err
	}

	// Step 2: If nothing was dropped, return unchanged.
	if len(retained) == len(messages) {
		return messages, nil
	}

	// Step 3: Compute dropped messages (prefix diff).
	// The fallback policy preserves a suffix of the input. The dropped
	// messages are the prefix that was removed.
	droppedCount := len(messages) - len(retained)
	dropped := messages[:droppedCount]

	// Step 4: Summarize the dropped messages.
	summaryMsg, err := m.summarizer.Summarize(ctx, dropped)
	if err != nil {
		// Step 5: Graceful degradation — return fallback result on error.
		return retained, nil
	}

	// Step 6: Mark the summary message as synthetic.
	if summaryMsg.Metadata == nil {
		summaryMsg.Metadata = make(map[string]any)
	}
	summaryMsg.Metadata["bond:synthetic"] = true

	// Step 7: Find preamble in retained (messages before first user message).
	preambleEnd := 0
	for i, msg := range retained {
		if msg.Role == bond.RoleUser {
			preambleEnd = i
			break
		}
		// If no user message found, everything is preamble.
		if i == len(retained)-1 {
			preambleEnd = len(retained)
		}
	}

	preamble := retained[:preambleEnd]
	rest := retained[preambleEnd:]

	// Step 8: Return preamble + summaryMsg + rest_of_retained.
	result := make([]bond.Message, 0, len(preamble)+1+len(rest))
	result = append(result, preamble...)
	result = append(result, summaryMsg)
	result = append(result, rest...)

	return result, nil
}

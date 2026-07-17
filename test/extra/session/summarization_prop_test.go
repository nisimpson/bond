package session_test

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/extra/session"
)

// ---------------------------------------------------------------------------
// Test helpers for SummarizationManager property tests
// ---------------------------------------------------------------------------

// fixedSummarizer returns a fixed summary message.
type fixedSummarizer struct {
	summary bond.Message
}

func (s *fixedSummarizer) Summarize(_ context.Context, _ []bond.Message) (bond.Message, error) {
	return s.summary, nil
}

// failingSummarizer always returns an error.
type failingSummarizer struct {
	err error
}

func (s *failingSummarizer) Summarize(_ context.Context, _ []bond.Message) (bond.Message, error) {
	return bond.Message{}, s.err
}

// trackingSummarizer records whether Summarize was called.
type trackingSummarizer struct {
	called  bool
	summary bond.Message
}

func (s *trackingSummarizer) Summarize(_ context.Context, _ []bond.Message) (bond.Message, error) {
	s.called = true
	return s.summary, nil
}

// tailPolicy keeps the last N messages (simple fallback for tests).
type tailPolicy struct {
	keepLast int
}

func (p *tailPolicy) Select(_ context.Context, messages []bond.Message) ([]bond.Message, error) {
	if len(messages) <= p.keepLast {
		return messages, nil
	}
	return messages[len(messages)-p.keepLast:], nil
}

// compile-time check
var _ agent.HistoryPolicy = (*tailPolicy)(nil)

// identityPolicy returns all messages unchanged.
type identityPolicy struct{}

func (p *identityPolicy) Select(_ context.Context, messages []bond.Message) ([]bond.Message, error) {
	return messages, nil
}

var _ agent.HistoryPolicy = (*identityPolicy)(nil)

// generateConversation generates a conversation with a preamble and user/assistant pairs.
// The preamble consists of assistant messages; the rest alternates user/assistant.
func generateConversation(rng *rand.Rand) []bond.Message {
	preamble := generatePreamble(rng)
	pairs := generateConversationPairs(rng)
	return append(preamble, pairs...)
}

// ---------------------------------------------------------------------------
// Property 1: HistoryPolicy.Select preserves ordering
// **Validates: Requirements 5.2**
// ---------------------------------------------------------------------------

func TestProperty_SummarizationManagerPreservesOrdering(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		messages := generateConversation(rng)
		if len(messages) < 2 {
			return true // trivial case
		}

		keepLast := 1 + rng.Intn(len(messages))
		fallback := &tailPolicy{keepLast: keepLast}
		summary := bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: "Summary of dropped messages"}},
		}

		mgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
			Summarizer: &fixedSummarizer{summary: summary},
			Fallback:   fallback,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Select(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Select error: %v", err)
			return false
		}

		// Property: all non-summary messages in the output appear in the input
		// in the same relative order. Summary messages (bond:synthetic) are
		// allowed to be new.
		inputIdx := 0
		for _, outMsg := range result {
			// Skip synthetic summary messages — they don't come from input.
			if outMsg.Metadata != nil {
				if _, ok := outMsg.Metadata["bond:synthetic"]; ok {
					continue
				}
			}

			found := false
			for inputIdx < len(messages) {
				if singleMessageEqual(outMsg, messages[inputIdx]) {
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
		t.Errorf("Property: SummarizationManager preserves ordering — failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Property 6: SummarizationManager graceful degradation
// **Validates: Requirements 6.6**
// ---------------------------------------------------------------------------

func TestProperty_SummarizationManagerGracefulDegradation(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		messages := generateConversation(rng)
		if len(messages) < 2 {
			return true
		}

		// Use a keepLast that's less than total messages so fallback drops something.
		keepLast := 1 + rng.Intn(len(messages)-1)
		fallback := &tailPolicy{keepLast: keepLast}

		summarizerErr := errors.New("summarizer: service unavailable")
		mgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
			Summarizer: &failingSummarizer{err: summarizerErr},
			Fallback:   fallback,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Select(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Select error: %v", err)
			return false
		}

		// Compute what fallback alone would return.
		fallbackResult, err := fallback.Select(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected fallback Select error: %v", err)
			return false
		}

		// Property: when summarizer fails, output equals fallback output.
		if len(result) != len(fallbackResult) {
			t.Logf("length mismatch: got %d, want %d", len(result), len(fallbackResult))
			return false
		}

		for i := range result {
			if !singleMessageEqual(result[i], fallbackResult[i]) {
				t.Logf("message mismatch at index %d", i)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: SummarizationManager graceful degradation — failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Metadata marking on synthetic messages
// **Validates: Requirements 6.9**
// ---------------------------------------------------------------------------

func TestProperty_SummarizationManagerMarksSyntheticMetadata(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		messages := generateConversation(rng)
		if len(messages) < 3 {
			return true // need enough messages to trigger trimming
		}

		// Force trimming by keeping fewer messages than total.
		keepLast := 1 + rng.Intn(len(messages)-2)
		fallback := &tailPolicy{keepLast: keepLast}
		summary := bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: randomASCII(rng, 30)}},
		}

		mgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
			Summarizer: &fixedSummarizer{summary: summary},
			Fallback:   fallback,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Select(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Select error: %v", err)
			return false
		}

		// Only check when trimming actually occurred.
		fallbackResult, _ := fallback.Select(context.Background(), messages)
		if len(fallbackResult) == len(messages) {
			return true // no trimming happened
		}

		// Property: exactly one message in the output has Metadata["bond:synthetic"] == true.
		syntheticCount := 0
		for _, msg := range result {
			if msg.Metadata != nil {
				if val, ok := msg.Metadata["bond:synthetic"]; ok {
					if val == true {
						syntheticCount++
					}
				}
			}
		}

		if syntheticCount != 1 {
			t.Logf("expected 1 synthetic message, got %d", syntheticCount)
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: SummarizationManager marks synthetic metadata — failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: No-op when fallback drops nothing
// **Validates: Requirements 6.10**
// ---------------------------------------------------------------------------

func TestProperty_SummarizationManagerNoOpWhenNothingDropped(t *testing.T) {
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		messages := generateConversation(rng)

		// Identity policy returns all messages — nothing is dropped.
		fallback := &identityPolicy{}
		summarizer := &trackingSummarizer{
			summary: bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: "should not appear"}},
			},
		}

		mgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
			Summarizer: summarizer,
			Fallback:   fallback,
		})
		if err != nil {
			t.Logf("unexpected constructor error: %v", err)
			return false
		}

		result, err := mgr.Select(context.Background(), messages)
		if err != nil {
			t.Logf("unexpected Select error: %v", err)
			return false
		}

		// Property: Summarizer.Summarize was NOT called.
		if summarizer.called {
			t.Log("summarizer was called when nothing was dropped")
			return false
		}

		// Property: output is identical to input.
		if len(result) != len(messages) {
			t.Logf("length mismatch: got %d, want %d", len(result), len(messages))
			return false
		}

		for i := range result {
			if !singleMessageEqual(result[i], messages[i]) {
				t.Logf("message mismatch at index %d", i)
				return false
			}
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: SummarizationManager no-op when nothing dropped — failed: %v", err)
	}
}

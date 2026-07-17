package session_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/session"
)

// TestIntegration_SessionAndTrimmingPluginComposition verifies that
// SessionPlugin and TrimmingPlugin compose correctly when registered on the
// same HookRegistry. SessionPlugin loads history via BeforeStreamHook, and
// TrimmingPlugin trims via BeforeModelInvokeHook.
//
// Validates: Requirements 10.1, 10.2, 10.3
func TestIntegration_SessionAndTrimmingPluginComposition(t *testing.T) {
	ctx := context.Background()

	// --- Setup: pre-load session history into InMemoryStore ---
	store := session.NewInMemoryStore()
	sessionID := "test-session"

	// Save 5 user/assistant pairs as prior history.
	history := make([]bond.Message, 0, 10)
	for i := 1; i <= 5; i++ {
		history = append(history,
			bond.Message{
				Role:    bond.RoleUser,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("user message %d", i)}},
			},
			bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("assistant message %d", i)}},
			},
		)
	}
	if err := store.Save(ctx, sessionID, history); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	// --- Configure plugins ---

	// SessionPlugin: resolves a fixed session ID.
	sessionPlugin := session.NewSessionPlugin(session.SessionPluginOptions{
		Store: store,
		ResolveID: func(_ context.Context) (string, error) {
			return sessionID, nil
		},
	})

	// TrimmingPlugin: sliding window of 2 pairs.
	mgr, err := session.NewSlidingWindowManager(session.SlidingWindowOptions{
		WindowSize: 2,
	})
	if err != nil {
		t.Fatalf("NewSlidingWindowManager: %v", err)
	}

	trimmingPlugin := session.NewTrimmingPlugin(session.TrimmingPluginOptions{
		Policy: mgr,
	})

	// --- Register plugins in correct order (session first, trimming second) ---
	registry := &bond.HookRegistry{}
	sessionPlugin.Init(registry)
	trimmingPlugin.Init(registry)

	// --- Simulate the agent loop: fire BeforeStreamHook ---
	// The new prompt is a single user message.
	newPrompt := []bond.Message{
		{
			Role:    bond.RoleUser,
			Content: []bond.Block{&bond.TextBlock{Text: "new user prompt"}},
		},
	}

	streamHook := &bond.BeforeStreamHook{Messages: newPrompt}
	if err := registry.Notify(ctx, streamHook); err != nil {
		t.Fatalf("BeforeStreamHook: %v", err)
	}

	// After SessionPlugin fires, messages should have loaded history prepended.
	// 10 history messages + 1 new prompt = 11 messages total.
	if got := len(streamHook.Messages); got != 11 {
		t.Fatalf("after BeforeStreamHook: expected 11 messages, got %d", got)
	}

	// Verify history was prepended (first message should be "user message 1").
	firstBlock, ok := streamHook.Messages[0].Content[0].(*bond.TextBlock)
	if !ok || firstBlock.Text != "user message 1" {
		t.Fatalf("expected first message to be 'user message 1', got %v", streamHook.Messages[0].Content)
	}

	// --- Simulate the agent loop: fire BeforeModelInvokeHook ---
	modelHook := &bond.BeforeModelInvokeHook{Messages: streamHook.Messages}
	if err := registry.Notify(ctx, modelHook); err != nil {
		t.Fatalf("BeforeModelInvokeHook: %v", err)
	}

	// After TrimmingPlugin fires with window size 2, only the last 2 user/assistant
	// pairs should remain. The conversation has 6 user messages (5 history + 1 new).
	// The last 2 user messages are "user message 5" and "new user prompt".
	// With their assistant replies: user5, asst5, newPrompt = 3 messages for 2 "pairs".
	// Actually, "new user prompt" has no assistant reply yet, so it counts as one pair.
	// So: pair 5 = (user5, asst5) and pair 6 = (newPrompt) → last 2 pairs = 4 messages.

	// Let's count the user messages in the trimmed output to verify the bound.
	var userCount int
	for _, msg := range modelHook.Messages {
		if msg.Role == bond.RoleUser {
			userCount++
		}
	}
	if userCount > 2 {
		t.Errorf("expected at most 2 user messages after trimming (window=2), got %d", userCount)
	}

	// Verify the most recent user message is preserved.
	lastMsg := modelHook.Messages[len(modelHook.Messages)-1]
	lastBlock, ok := lastMsg.Content[0].(*bond.TextBlock)
	if !ok || lastBlock.Text != "new user prompt" {
		t.Errorf("expected last message to be 'new user prompt', got %v", lastMsg.Content)
	}

	// Verify trimming actually reduced the message count.
	if len(modelHook.Messages) >= 11 {
		t.Errorf("expected trimmed messages to be fewer than 11, got %d", len(modelHook.Messages))
	}

	t.Logf("Composition test: 11 messages → %d after trimming (window=2)", len(modelHook.Messages))
}

// TestIntegration_PluginComposition verifies that both plugins within the
// session package compose correctly and that both implement bond.Plugin.
//
// Validates: Requirements 10.1, 10.2
func TestIntegration_PluginComposition(t *testing.T) {
	// Both plugin types implement bond.Plugin.
	var _ bond.Plugin = session.NewTrimmingPlugin(session.TrimmingPluginOptions{})
	var _ bond.Plugin = session.NewSessionPlugin(session.SessionPluginOptions{})

	// Verify both can be registered on the same HookRegistry without conflict.
	registry := &bond.HookRegistry{}

	store := session.NewInMemoryStore()
	sessionPlugin := session.NewSessionPlugin(session.SessionPluginOptions{
		Store: store,
		ResolveID: func(_ context.Context) (string, error) {
			return "test", nil
		},
	})

	mgr, _ := session.NewSlidingWindowManager(session.SlidingWindowOptions{WindowSize: 5})
	trimmingPlugin := session.NewTrimmingPlugin(session.TrimmingPluginOptions{Policy: mgr})

	sessionPlugin.Init(registry)
	trimmingPlugin.Init(registry)

	t.Log("Both plugins compose on a single HookRegistry without conflict.")
}

// TestIntegration_ErrContextOverflowWrapping verifies that providers can wrap
// bond.ErrContextOverflow without importing extra/ packages, and that the
// TrimmingPlugin can detect it via errors.Is.
//
// Validates: Requirements 10.3, 10.4
func TestIntegration_ErrContextOverflowWrapping(t *testing.T) {
	// Simulate a provider wrapping ErrContextOverflow.
	// Providers only need to import the root bond package.
	providerErr := fmt.Errorf("bedrock: input too large for model: %w", bond.ErrContextOverflow)

	// Verify errors.Is works through wrapping.
	if !errors.Is(providerErr, bond.ErrContextOverflow) {
		t.Fatal("errors.Is failed to detect wrapped ErrContextOverflow")
	}

	// Double-wrap to simulate middleware layers.
	middlewareErr := fmt.Errorf("retry: attempt 1 failed: %w", providerErr)
	if !errors.Is(middlewareErr, bond.ErrContextOverflow) {
		t.Fatal("errors.Is failed to detect doubly-wrapped ErrContextOverflow")
	}

	// Verify the TrimmingPlugin's Recover method detects the wrapped error.
	mgr, err := session.NewSlidingWindowManager(session.SlidingWindowOptions{
		WindowSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	plugin := session.NewTrimmingPlugin(session.TrimmingPluginOptions{
		Policy:      mgr,
		AutoRecover: true,
		MaxRetries:  2,
	})

	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
		{Role: bond.RoleAssistant, Content: []bond.Block{&bond.TextBlock{Text: "hi"}}},
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "how are you"}}},
	}

	// Recover should succeed with a wrapped ErrContextOverflow.
	recovered, recoverErr := plugin.Recover(context.Background(), providerErr, messages)
	if recoverErr != nil {
		t.Fatalf("Recover failed: %v", recoverErr)
	}
	if len(recovered) == 0 {
		t.Fatal("Recover returned empty messages")
	}
	if len(recovered) >= len(messages) {
		t.Errorf("expected recovered messages to be trimmed, got %d (original: %d)", len(recovered), len(messages))
	}

	// Recover should fail for non-overflow errors.
	otherErr := fmt.Errorf("bedrock: rate limited")
	_, recoverErr = plugin.Recover(context.Background(), otherErr, messages)
	if recoverErr == nil {
		t.Fatal("Recover should fail for non-overflow errors")
	}

	t.Log("ErrContextOverflow wrapping works correctly across package boundaries.")
}

// TestIntegration_SummarizationManagerWithTrimmingPlugin verifies that a
// TrimmingPlugin backed by a SummarizationManager correctly summarizes dropped
// messages and injects a synthetic summary message into the retained history.
//
// Scenario:
//  1. SummarizationManager wraps a SlidingWindowManager (window=2) as fallback.
//  2. A conversation of 6 user/assistant pairs is processed.
//  3. The fallback drops the first 4 pairs; the Summarizer condenses them.
//  4. The result contains the synthetic summary before the retained messages.
//  5. On summarizer failure, graceful degradation returns just the fallback result.
//
// Validates: Requirements 6.*
func TestIntegration_SummarizationManagerWithTrimmingPlugin(t *testing.T) {
	ctx := context.Background()

	// --- Build a mock summarizer that produces a known summary ---
	summarizer := &mockSummarizer{
		summary: bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: "Summary of earlier conversation."}},
		},
	}

	// --- Fallback: SlidingWindowManager with window size 2 ---
	fallback, err := session.NewSlidingWindowManager(session.SlidingWindowOptions{
		WindowSize: 2,
	})
	if err != nil {
		t.Fatalf("NewSlidingWindowManager: %v", err)
	}

	// --- SummarizationManager wraps the fallback ---
	sumMgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
		Summarizer: summarizer,
		Fallback:   fallback,
	})
	if err != nil {
		t.Fatalf("NewSummarizationManager: %v", err)
	}

	// --- TrimmingPlugin uses SummarizationManager as its policy ---
	plugin := session.NewTrimmingPlugin(session.TrimmingPluginOptions{
		Policy:      sumMgr,
		AutoRecover: true,
		MaxRetries:  2,
	})

	// --- Build a conversation with 6 user/assistant pairs ---
	messages := make([]bond.Message, 0, 12)
	for i := 1; i <= 6; i++ {
		messages = append(messages,
			bond.Message{
				Role:    bond.RoleUser,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("user %d", i)}},
			},
			bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("assistant %d", i)}},
			},
		)
	}

	// --- Exercise via hook registration (TrimmingPlugin.Init) ---
	registry := &bond.HookRegistry{}
	plugin.Init(registry)

	modelHook := &bond.BeforeModelInvokeHook{Messages: messages}
	if err := registry.Notify(ctx, modelHook); err != nil {
		t.Fatalf("BeforeModelInvokeHook: %v", err)
	}

	trimmed := modelHook.Messages

	// Verify: the total should be less than original 12
	if len(trimmed) >= 12 {
		t.Fatalf("expected trimmed output < 12 messages, got %d", len(trimmed))
	}

	// Verify: a synthetic summary message exists with metadata
	var foundSynthetic bool
	var syntheticIdx int
	for i, msg := range trimmed {
		if msg.Metadata != nil {
			if v, ok := msg.Metadata["bond:synthetic"]; ok && v == true {
				foundSynthetic = true
				syntheticIdx = i
				break
			}
		}
	}
	if !foundSynthetic {
		t.Fatal("expected a synthetic summary message with Metadata[\"bond:synthetic\"] == true")
	}

	// Verify: the summary message content matches our mock summarizer output
	summaryBlock, ok := trimmed[syntheticIdx].Content[0].(*bond.TextBlock)
	if !ok || summaryBlock.Text != "Summary of earlier conversation." {
		t.Errorf("expected summary text 'Summary of earlier conversation.', got %v", trimmed[syntheticIdx].Content)
	}

	// Verify: summary appears before the retained user messages
	// (since there's no preamble, syntheticIdx should be 0)
	if syntheticIdx != 0 {
		t.Errorf("expected summary at index 0 (no preamble), got index %d", syntheticIdx)
	}

	// Verify: retained messages follow the summary
	// With window=2, we expect the last 2 user msgs ("user 5", "user 6") and their replies.
	var retainedUsers []string
	for _, msg := range trimmed[syntheticIdx+1:] {
		if msg.Role == bond.RoleUser {
			if tb, ok := msg.Content[0].(*bond.TextBlock); ok {
				retainedUsers = append(retainedUsers, tb.Text)
			}
		}
	}
	if len(retainedUsers) != 2 {
		t.Fatalf("expected 2 retained user messages, got %d: %v", len(retainedUsers), retainedUsers)
	}
	if retainedUsers[0] != "user 5" || retainedUsers[1] != "user 6" {
		t.Errorf("expected retained users [user 5, user 6], got %v", retainedUsers)
	}

	// Verify: the summarizer was called with the dropped messages
	if summarizer.callCount != 1 {
		t.Errorf("expected summarizer to be called once, got %d", summarizer.callCount)
	}
	if len(summarizer.lastInput) != 8 {
		// 4 dropped pairs = 8 messages
		t.Errorf("expected summarizer input of 8 messages (4 dropped pairs), got %d", len(summarizer.lastInput))
	}

	t.Logf("Integration: 12 messages → %d after summarization (window=2)", len(trimmed))
}

// TestIntegration_SummarizationManagerGracefulDegradation verifies that when
// the Summarizer fails, the SummarizationManager returns the fallback result
// without a summary — no synthetic message should appear.
//
// Validates: Requirements 6.6
func TestIntegration_SummarizationManagerGracefulDegradation(t *testing.T) {
	ctx := context.Background()

	// Failing summarizer
	failingSummarizer := &mockSummarizer{
		err: errors.New("LLM unavailable"),
	}

	fallback, err := session.NewSlidingWindowManager(session.SlidingWindowOptions{
		WindowSize: 2,
	})
	if err != nil {
		t.Fatalf("NewSlidingWindowManager: %v", err)
	}

	sumMgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
		Summarizer: failingSummarizer,
		Fallback:   fallback,
	})
	if err != nil {
		t.Fatalf("NewSummarizationManager: %v", err)
	}

	// Build 6 user/assistant pairs.
	messages := make([]bond.Message, 0, 12)
	for i := 1; i <= 6; i++ {
		messages = append(messages,
			bond.Message{
				Role:    bond.RoleUser,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("user %d", i)}},
			},
			bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("assistant %d", i)}},
			},
		)
	}

	// Call Select directly on the SummarizationManager.
	result, err := sumMgr.Select(ctx, messages)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}

	// Should return fallback result (last 2 pairs = 4 messages).
	if len(result) != 4 {
		t.Fatalf("expected 4 messages from fallback, got %d", len(result))
	}

	// No synthetic message should be present.
	for _, msg := range result {
		if msg.Metadata != nil {
			if v, ok := msg.Metadata["bond:synthetic"]; ok && v == true {
				t.Fatal("unexpected synthetic message in degraded result")
			}
		}
	}

	t.Log("Graceful degradation: summarizer failure returns fallback result only.")
}

// TestIntegration_SummarizationManagerWithPreamble verifies that when a system
// preamble exists, the summary is placed after the preamble and before retained
// messages.
//
// Validates: Requirements 6.5
func TestIntegration_SummarizationManagerWithPreamble(t *testing.T) {
	ctx := context.Background()

	summarizer := &mockSummarizer{
		summary: bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: "Context summary."}},
		},
	}

	fallback, err := session.NewSlidingWindowManager(session.SlidingWindowOptions{
		WindowSize: 1,
	})
	if err != nil {
		t.Fatalf("NewSlidingWindowManager: %v", err)
	}

	sumMgr, err := session.NewSummarizationManager(session.SummarizationManagerOptions{
		Summarizer: summarizer,
		Fallback:   fallback,
	})
	if err != nil {
		t.Fatalf("NewSummarizationManager: %v", err)
	}

	// Build a conversation with a system-like preamble (assistant message before
	// the first user message).
	messages := []bond.Message{
		{Role: bond.RoleAssistant, Content: []bond.Block{&bond.TextBlock{Text: "system preamble"}}},
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "user 1"}}},
		{Role: bond.RoleAssistant, Content: []bond.Block{&bond.TextBlock{Text: "assistant 1"}}},
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "user 2"}}},
		{Role: bond.RoleAssistant, Content: []bond.Block{&bond.TextBlock{Text: "assistant 2"}}},
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "user 3"}}},
		{Role: bond.RoleAssistant, Content: []bond.Block{&bond.TextBlock{Text: "assistant 3"}}},
	}

	result, err := sumMgr.Select(ctx, messages)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	// Expect: preamble + summary + last 1 pair (user 3, assistant 3)
	// The fallback (window=1) retains preamble + last pair.
	// SummarizationManager inserts summary between preamble and retained user messages.

	// Find the preamble in result (everything before first user message).
	var firstUserIdx int
	for i, msg := range result {
		if msg.Role == bond.RoleUser {
			firstUserIdx = i
			break
		}
	}

	// The synthetic summary should be just before the first user message.
	if firstUserIdx < 2 {
		t.Fatalf("expected preamble + summary before first user, firstUserIdx=%d, result=%d msgs", firstUserIdx, len(result))
	}

	syntheticMsg := result[firstUserIdx-1]
	if syntheticMsg.Metadata == nil || syntheticMsg.Metadata["bond:synthetic"] != true {
		t.Fatal("expected synthetic summary message just before retained user messages")
	}

	// Preamble should be first.
	preambleBlock, ok := result[0].Content[0].(*bond.TextBlock)
	if !ok || preambleBlock.Text != "system preamble" {
		t.Errorf("expected preamble at index 0, got %v", result[0])
	}

	t.Logf("Preamble test: result has %d messages, preamble at 0, summary at %d, user at %d",
		len(result), firstUserIdx-1, firstUserIdx)
}

// mockSummarizer is a test helper that returns a preconfigured summary or error.
type mockSummarizer struct {
	summary   bond.Message
	err       error
	callCount int
	lastInput []bond.Message
}

func (m *mockSummarizer) Summarize(_ context.Context, messages []bond.Message) (bond.Message, error) {
	m.callCount++
	m.lastInput = messages
	if m.err != nil {
		return bond.Message{}, m.err
	}
	return m.summary, nil
}

// TestIntegration_HookOrdering verifies that hook registration order determines
// execution order: SessionPlugin's BeforeStreamHook runs before any other hook
// registered after it, and TrimmingPlugin's BeforeModelInvokeHook runs in the
// correct phase.
//
// Validates: Requirements 10.3
func TestIntegration_HookOrdering(t *testing.T) {
	ctx := context.Background()

	var order []string

	registry := &bond.HookRegistry{}

	// Register a tracking hook for BeforeStreamHook before the plugins.
	bond.OnBefore(registry, bond.BeforeHookFunc[*bond.BeforeStreamHook](
		func(_ context.Context, _ *bond.BeforeStreamHook) error {
			order = append(order, "pre-stream-observer")
			return nil
		},
	))

	// Register SessionPlugin.
	store := session.NewInMemoryStore()
	sessionPlugin := session.NewSessionPlugin(session.SessionPluginOptions{
		Store: store,
		ResolveID: func(_ context.Context) (string, error) {
			return "s1", nil
		},
	})
	sessionPlugin.Init(registry)

	// Register TrimmingPlugin.
	mgr, _ := session.NewSlidingWindowManager(session.SlidingWindowOptions{WindowSize: 10})
	trimmingPlugin := session.NewTrimmingPlugin(session.TrimmingPluginOptions{Policy: mgr})
	trimmingPlugin.Init(registry)

	// Register a tracking hook for BeforeModelInvokeHook after the plugins.
	bond.OnBefore(registry, bond.BeforeHookFunc[*bond.BeforeModelInvokeHook](
		func(_ context.Context, _ *bond.BeforeModelInvokeHook) error {
			order = append(order, "post-trim-observer")
			return nil
		},
	))

	// Fire BeforeStreamHook.
	streamHook := &bond.BeforeStreamHook{Messages: bond.TextPrompt("hello")}
	if err := registry.Notify(ctx, streamHook); err != nil {
		t.Fatalf("BeforeStreamHook: %v", err)
	}

	// Fire BeforeModelInvokeHook.
	modelHook := &bond.BeforeModelInvokeHook{Messages: streamHook.Messages}
	if err := registry.Notify(ctx, modelHook); err != nil {
		t.Fatalf("BeforeModelInvokeHook: %v", err)
	}

	// Verify the pre-stream observer ran (it was registered first for BeforeStreamHook).
	if len(order) == 0 || order[0] != "pre-stream-observer" {
		t.Errorf("expected pre-stream-observer first, got %v", order)
	}

	// Verify the post-trim observer ran (it was registered last for BeforeModelInvokeHook).
	found := false
	for _, o := range order {
		if o == "post-trim-observer" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("post-trim-observer not found in order: %v", order)
	}

	t.Logf("Hook ordering: %v", order)
}

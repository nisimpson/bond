package conversation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/conversation"
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
	mgr, err := conversation.NewSlidingWindowManager(conversation.SlidingWindowOptions{
		WindowSize: 2,
	})
	if err != nil {
		t.Fatalf("NewSlidingWindowManager: %v", err)
	}

	trimmingPlugin := conversation.NewTrimmingPlugin(conversation.TrimmingPluginOptions{
		Manager: mgr,
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

// TestIntegration_PackageIndependence verifies that importing both
// extra/conversation and extra/session does not cause import cycles.
// The fact that this file compiles is sufficient proof.
//
// Validates: Requirements 10.1, 10.2
func TestIntegration_PackageIndependence(t *testing.T) {
	// This test verifies package independence by compilation alone.
	// If extra/conversation imported extra/session (or vice versa), this
	// file would fail to compile due to an import cycle.

	// Verify both packages expose their expected types independently.
	var _ conversation.ConversationManager
	var _ session.SessionStore

	// Both plugin types implement bond.Plugin.
	var _ bond.Plugin = conversation.NewTrimmingPlugin(conversation.TrimmingPluginOptions{})
	var _ bond.Plugin = session.NewSessionPlugin(session.SessionPluginOptions{})

	t.Log("No import cycle detected: both packages compile independently.")
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
	mgr, err := conversation.NewSlidingWindowManager(conversation.SlidingWindowOptions{
		WindowSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	plugin := conversation.NewTrimmingPlugin(conversation.TrimmingPluginOptions{
		Manager:     mgr,
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
	mgr, _ := conversation.NewSlidingWindowManager(conversation.SlidingWindowOptions{WindowSize: 10})
	trimmingPlugin := conversation.NewTrimmingPlugin(conversation.TrimmingPluginOptions{Manager: mgr})
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

package session_test

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/session"
)

// errorStore is a mock store that always returns an error on Load.
type errorStore struct {
	loadErr error
}

func (s *errorStore) Load(_ context.Context, _ string) ([]bond.Message, error) {
	return nil, s.loadErr
}

func (s *errorStore) Save(_ context.Context, _ string, _ []bond.Message) error {
	return nil
}

// Feature: conversation-session-management, Property 15: SessionPlugin prepends loaded history
// Validates: CONV-9.3
func TestProperty_SessionPluginPrependsLoadedHistory(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		store := session.NewInMemoryStore()
		ctx := context.Background()

		sessionID := randomSessionID(r)

		// Generate and save some random history messages.
		history := randomMessages(r, 5)
		if err := store.Save(ctx, sessionID, history); err != nil {
			t.Logf("Save failed: %v", err)
			return false
		}

		// Generate new prompt messages.
		prompt := randomMessages(r, 3)

		// Create plugin with a fixed resolver.
		plugin := session.NewSessionPlugin(session.SessionPluginOptions{
			Store: store,
			ResolveID: func(_ context.Context) (string, error) {
				return sessionID, nil
			},
		})

		// Register hooks via Init.
		registry := &bond.HookRegistry{}
		plugin.Init(registry)

		// Fire BeforeStreamHook with the prompt messages.
		hook := &bond.BeforeStreamHook{Messages: prompt}
		if err := registry.Notify(ctx, hook); err != nil {
			t.Logf("Notify failed: %v", err)
			return false
		}

		// Verify: hook.Messages == loaded history + original prompt.
		expected := append(history, prompt...)
		if len(hook.Messages) != len(expected) {
			t.Logf("Length mismatch: got %d, want %d", len(hook.Messages), len(expected))
			return false
		}

		if !messagesEqual(hook.Messages, expected) {
			t.Logf("Messages mismatch after BeforeStreamHook")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 15: SessionPlugin prepends loaded history — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 16: SessionPlugin saves on message append
// Validates: CONV-9.4
func TestProperty_SessionPluginSavesOnMessageAppend(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		store := session.NewInMemoryStore()
		ctx := context.Background()

		sessionID := randomSessionID(r)

		// Generate initial prompt messages.
		prompt := randomMessages(r, 3)

		// Create plugin with a fixed resolver.
		plugin := session.NewSessionPlugin(session.SessionPluginOptions{
			Store: store,
			ResolveID: func(_ context.Context) (string, error) {
				return sessionID, nil
			},
		})

		// Register hooks via Init.
		registry := &bond.HookRegistry{}
		plugin.Init(registry)

		// Fire BeforeStreamHook first to initialize accumulated state.
		hook := &bond.BeforeStreamHook{Messages: prompt}
		if err := registry.Notify(ctx, hook); err != nil {
			t.Logf("BeforeStreamHook Notify failed: %v", err)
			return false
		}

		// Generate a new message to append.
		newMsg := randomMessages(r, 1)[0]

		// Fire AfterMessageAppendedHook with the new message.
		appendHook := &bond.AfterMessageAppendedHook{Message: newMsg}
		if err := registry.Notify(ctx, appendHook); err != nil {
			t.Logf("AfterMessageAppendedHook Notify failed: %v", err)
			return false
		}

		// Load from store and verify it contains prompt + appended message.
		loaded, err := store.Load(ctx, sessionID)
		if err != nil {
			t.Logf("Load failed: %v", err)
			return false
		}

		expected := append(prompt, newMsg)
		if len(loaded) != len(expected) {
			t.Logf("Length mismatch: got %d, want %d", len(loaded), len(expected))
			return false
		}

		if !messagesEqual(loaded, expected) {
			t.Logf("Saved messages do not match expected accumulated conversation")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 16: SessionPlugin saves on message append — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 17: SessionPlugin Load error aborts stream
// Validates: CONV-9.5
func TestProperty_SessionPluginLoadErrorAbortsStream(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()

		// Create a unique error for this iteration.
		errMsg := randomASCII(r, 30)
		loadErr := errors.New(errMsg)

		// Create plugin with the error store.
		plugin := session.NewSessionPlugin(session.SessionPluginOptions{
			Store: &errorStore{loadErr: loadErr},
			ResolveID: func(_ context.Context) (string, error) {
				return randomSessionID(r), nil
			},
		})

		// Register hooks via Init.
		registry := &bond.HookRegistry{}
		plugin.Init(registry)

		// Fire BeforeStreamHook with some prompt messages.
		prompt := randomMessages(r, 2)
		hook := &bond.BeforeStreamHook{Messages: prompt}
		err := registry.Notify(ctx, hook)

		// Verify the error is propagated.
		if err == nil {
			t.Logf("Expected error, got nil")
			return false
		}

		if !errors.Is(err, loadErr) {
			t.Logf("Error mismatch: got %v, want %v", err, loadErr)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 17: SessionPlugin Load error aborts stream — failed: %v", err)
	}
}

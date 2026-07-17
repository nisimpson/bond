package session_test

import (
	"context"
	"encoding/json"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/session"
)

// randomRole returns a random message role.
func randomRole(r *rand.Rand) bond.Role {
	if r.Intn(2) == 0 {
		return bond.RoleUser
	}
	return bond.RoleAssistant
}

// randomASCII generates a random ASCII string of length 1..maxLen.
func randomASCII(r *rand.Rand, maxLen int) string {
	n := r.Intn(maxLen) + 1
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(32 + r.Intn(95)) // printable ASCII
	}
	return string(b)
}

// randomBlock generates a random block (TextBlock, ToolUseBlock, or ToolResultBlock).
func randomBlock(r *rand.Rand) bond.Block {
	switch r.Intn(3) {
	case 0:
		return &bond.TextBlock{Text: randomASCII(r, 50)}
	case 1:
		input, _ := json.Marshal(map[string]string{"key": randomASCII(r, 10)})
		return &bond.ToolUseBlock{
			ID:    randomASCII(r, 10),
			Name:  randomASCII(r, 10),
			Input: json.RawMessage(input),
		}
	default:
		return &bond.ToolResultBlock{
			ToolUseID: randomASCII(r, 10),
			IsError:   r.Intn(2) == 0,
			Content:   []bond.Block{&bond.TextBlock{Text: randomASCII(r, 30)}},
		}
	}
}

// randomMetadata generates a random metadata map (possibly nil).
func randomMetadata(r *rand.Rand) map[string]any {
	if r.Intn(3) == 0 {
		return nil
	}
	m := make(map[string]any)
	n := r.Intn(3) + 1
	for range n {
		m[randomASCII(r, 8)] = randomASCII(r, 20)
	}
	return m
}

// randomMessages generates a slice of random messages.
func randomMessages(r *rand.Rand, maxCount int) []bond.Message {
	n := r.Intn(maxCount) + 1
	msgs := make([]bond.Message, n)
	for i := range msgs {
		blockCount := r.Intn(3) + 1
		blocks := make([]bond.Block, blockCount)
		for j := range blocks {
			blocks[j] = randomBlock(r)
		}
		msgs[i] = bond.Message{
			Role:     randomRole(r),
			Content:  blocks,
			Metadata: randomMetadata(r),
		}
	}
	return msgs
}

// randomSessionID generates a non-empty session ID string.
func randomSessionID(r *rand.Rand) string {
	return randomASCII(r, 20)
}

// messagesEqual compares two message slices for deep equality.
func messagesEqual(a, b []bond.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role {
			return false
		}
		if !reflect.DeepEqual(a[i].Metadata, b[i].Metadata) {
			return false
		}
		if len(a[i].Content) != len(b[i].Content) {
			return false
		}
		for j := range a[i].Content {
			if !reflect.DeepEqual(a[i].Content[j], b[i].Content[j]) {
				return false
			}
		}
	}
	return true
}

// Feature: conversation-session-management, Property 10: SessionStore Save/Load round-trip
// Validates: Requirements 6.4, 7.4, 7.5
func TestProperty_SessionStoreSaveLoadRoundTrip(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		store := session.NewInMemoryStore()
		ctx := context.Background()

		sessionID := randomSessionID(r)
		messages := randomMessages(r, 10)

		if err := store.Save(ctx, sessionID, messages); err != nil {
			t.Logf("Save failed: %v", err)
			return false
		}

		loaded, err := store.Load(ctx, sessionID)
		if err != nil {
			t.Logf("Load failed: %v", err)
			return false
		}

		if !messagesEqual(messages, loaded) {
			t.Logf("Round-trip mismatch for session %q", sessionID)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: SessionStore Save/Load round-trip — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 11: InMemoryStore isolation from caller mutation
// Validates: Requirements 7.4
func TestProperty_InMemoryStoreIsolationFromCallerMutation(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		store := session.NewInMemoryStore()
		ctx := context.Background()

		sessionID := randomSessionID(r)
		messages := randomMessages(r, 10)

		if err := store.Save(ctx, sessionID, messages); err != nil {
			t.Logf("Save failed: %v", err)
			return false
		}

		// Load once to capture the stored state.
		before, err := store.Load(ctx, sessionID)
		if err != nil {
			t.Logf("Load before mutation failed: %v", err)
			return false
		}

		// Mutate the original slice: change role, alter content, append a message.
		if len(messages) > 0 {
			messages[0].Role = "mutated"
			if len(messages[0].Content) > 0 {
				messages[0].Content[0] = &bond.TextBlock{Text: "MUTATED"}
			}
		}
		_ = append(messages, bond.Message{
			Role:    bond.RoleUser,
			Content: []bond.Block{&bond.TextBlock{Text: "extra"}},
		})

		// Load again — stored data should be unaffected.
		after, err := store.Load(ctx, sessionID)
		if err != nil {
			t.Logf("Load after mutation failed: %v", err)
			return false
		}

		if !messagesEqual(before, after) {
			t.Logf("Isolation violated: stored data changed after caller mutation for session %q", sessionID)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: InMemoryStore isolation from caller mutation — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 12: InMemoryStore concurrent access safety
// Validates: Requirements 7.2
func TestProperty_InMemoryStoreConcurrentAccessSafety(t *testing.T) {
	t.Parallel()

	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		store := session.NewInMemoryStore()
		ctx := context.Background()

		// Generate a pool of session IDs and messages for concurrent use.
		numSessions := r.Intn(5) + 2
		sessionIDs := make([]string, numSessions)
		for i := range sessionIDs {
			sessionIDs[i] = randomSessionID(r)
		}

		numGoroutines := r.Intn(10) + 5

		// Pre-generate seeds for each goroutine to avoid sharing the parent rand.
		seeds := make([]int64, numGoroutines)
		for i := range seeds {
			seeds[i] = r.Int63()
		}

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func() {
				defer wg.Done()
				localR := rand.New(rand.NewSource(seeds[i]))
				sid := sessionIDs[localR.Intn(len(sessionIDs))]

				// Alternate between Save and Load.
				for range 10 {
					if localR.Intn(2) == 0 {
						msgs := randomMessages(localR, 5)
						_ = store.Save(ctx, sid, msgs)
					} else {
						_, _ = store.Load(ctx, sid)
					}
				}
			}()
		}

		wg.Wait()
		return true // No panic or data race means success (race detector validates).
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: InMemoryStore concurrent access safety — failed: %v", err)
	}
}

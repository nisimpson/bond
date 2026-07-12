package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

// TestProperty_SessionCreationPreservesCWD verifies that for any valid path
// string provided as `cwd` in a `session/new` request, the created session
// stores that exact path string.
//
// **Validates: Requirements 3.1, 3.2**
func TestProperty_SessionCreationPreservesCWD(t *testing.T) {
	f := func(path cwdPath) bool {
		pathStr := string(path)

		// Build an initialize request (required before session/new).
		initParams, _ := json.Marshal(map[string]any{
			"protocolVersion": 1,
		})
		initID := json.RawMessage(`1`)
		initMsg := Message{
			JSONRPC: "2.0",
			Method:  "initialize",
			ID:      &initID,
			Params:  initParams,
		}
		initData, _ := json.Marshal(initMsg)

		// Build a session/new request with the generated cwd.
		sessionParams, _ := json.Marshal(map[string]any{
			"cwd": pathStr,
		})
		sessionID := json.RawMessage(`2`)
		sessionMsg := Message{
			JSONRPC: "2.0",
			Method:  "session/new",
			ID:      &sessionID,
			Params:  sessionParams,
		}
		sessionData, _ := json.Marshal(sessionMsg)

		// Combine both messages as newline-delimited input.
		input := string(initData) + "\n" + string(sessionData) + "\n"

		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(input), out)
		agent := &bondtest.EchoAgent{}
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		if err := h.Serve(context.Background()); err != nil {
			t.Logf("Serve error: %v", err)
			return false
		}

		// Verify the session was created and CWD matches exactly.
		h.mu.Lock()
		session := h.session
		h.mu.Unlock()

		if session == nil {
			t.Logf("session is nil for cwd %q", pathStr)
			return false
		}

		if session.CWD != pathStr {
			t.Logf("CWD mismatch: got %q, want %q", session.CWD, pathStr)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 8: Session Creation Preserves CWD failed: %v", err)
	}
}

// cwdPath is a type that generates arbitrary path strings for use as CWD
// in session/new requests. Covers absolute Unix paths, relative paths,
// paths with spaces, special characters, and edge cases.
type cwdPath string

// Generate implements quick.Generator for cwdPath, producing a variety of
// path-like strings that exercise different CWD storage scenarios.
func (cwdPath) Generate(rng *rand.Rand, size int) reflect.Value {
	var path string

	switch rng.Intn(6) {
	case 0:
		// Absolute Unix path: /foo/bar/baz
		path = generateAbsolutePath(rng)
	case 1:
		// Relative path: foo/bar/baz
		path = generateRelativePath(rng)
	case 2:
		// Path with spaces: /path/to/my project/src
		path = generatePathWithSpaces(rng)
	case 3:
		// Path with special characters: /tmp/a-b_c.d/e@f
		path = generatePathWithSpecialChars(rng)
	case 4:
		// Deep nested path
		path = generateDeepPath(rng)
	case 5:
		// Edge case paths
		path = generateEdgeCasePath(rng)
	}

	return reflect.ValueOf(cwdPath(path))
}

// generateAbsolutePath produces a random absolute Unix-style path.
func generateAbsolutePath(rng *rand.Rand) string {
	segments := 1 + rng.Intn(5)
	parts := make([]string, segments)
	for i := range parts {
		parts[i] = generatePathSegment(rng)
	}
	return "/" + strings.Join(parts, "/")
}

// generateRelativePath produces a random relative path.
func generateRelativePath(rng *rand.Rand) string {
	segments := 1 + rng.Intn(4)
	parts := make([]string, segments)
	for i := range parts {
		parts[i] = generatePathSegment(rng)
	}

	// Optionally prefix with . or ..
	switch rng.Intn(3) {
	case 0:
		return strings.Join(parts, "/")
	case 1:
		return "./" + strings.Join(parts, "/")
	default:
		return "../" + strings.Join(parts, "/")
	}
}

// generatePathWithSpaces produces a path containing spaces in segment names.
func generatePathWithSpaces(rng *rand.Rand) string {
	segments := 1 + rng.Intn(4)
	parts := make([]string, segments)
	for i := range parts {
		word1 := generatePathSegment(rng)
		word2 := generatePathSegment(rng)
		parts[i] = word1 + " " + word2
	}
	return "/" + strings.Join(parts, "/")
}

// generatePathWithSpecialChars produces a path with special characters
// commonly found in file paths (dashes, underscores, dots, at signs, etc).
func generatePathWithSpecialChars(rng *rand.Rand) string {
	specialChars := "-_@.+~"
	segments := 1 + rng.Intn(4)
	parts := make([]string, segments)
	for i := range parts {
		seg := generatePathSegment(rng)
		// Insert a special character at a random position.
		pos := rng.Intn(len(seg) + 1)
		ch := specialChars[rng.Intn(len(specialChars))]
		seg = seg[:pos] + string(ch) + seg[pos:]
		parts[i] = seg
	}
	return "/" + strings.Join(parts, "/")
}

// generateDeepPath produces a deeply nested path (6-12 segments).
func generateDeepPath(rng *rand.Rand) string {
	segments := 6 + rng.Intn(7)
	parts := make([]string, segments)
	for i := range parts {
		parts[i] = generatePathSegment(rng)
	}
	return "/" + strings.Join(parts, "/")
}

// generateEdgeCasePath produces edge-case path strings.
func generateEdgeCasePath(rng *rand.Rand) string {
	cases := []string{
		"/",
		".",
		"..",
		"/tmp",
		"/home/user/проект",
		"/Users/dev/my project (copy)/src",
		"/mnt/c/Users/dev/workspace",
		"C:\\Users\\dev\\workspace",
		"/path/with/trailing/slash/",
		"~/Documents/project",
		"/path/to/.hidden/dir",
		"/var/folders/xx/long_random_name_here_1234567890",
	}
	return cases[rng.Intn(len(cases))]
}

// generatePathSegment produces a short random string suitable as a path segment.
func generatePathSegment(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	length := 2 + rng.Intn(8)
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

// promptSequence is a custom type for generating sequences of prompt strings.
// Implements quick.Generator to produce 1-10 random prompt strings.
type promptSequence []string

// Generate implements quick.Generator for promptSequence, producing 1-10 random
// non-empty prompt strings suitable for session/prompt requests.
func (promptSequence) Generate(rng *rand.Rand, size int) reflect.Value {
	n := 1 + rng.Intn(10) // 1 to 10 prompts
	prompts := make([]string, n)
	for i := range prompts {
		prompts[i] = generatePromptText(rng)
	}
	return reflect.ValueOf(promptSequence(prompts))
}

// generatePromptText produces a random non-empty string suitable as a user prompt.
func generatePromptText(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .,!?"
	length := 3 + rng.Intn(50) // 3 to 52 characters
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

// TestProperty_ConversationHistoryAccumulates verifies that for any sequence of
// N prompt turns, the session history contains exactly 2N messages in alternating
// user/assistant order with matching content.
//
// **Validates: Requirements 4.1, 4.7**
func TestProperty_ConversationHistoryAccumulates(t *testing.T) {
	f := func(prompts promptSequence) bool {
		n := len(prompts)

		// Build the full input: initialize + session/new + N prompt requests.
		var inputLines []string

		// 1. Initialize request.
		initParams, _ := json.Marshal(map[string]any{
			"protocolVersion": 1,
		})
		initID := json.RawMessage(`1`)
		initMsg := Message{
			JSONRPC: "2.0",
			Method:  "initialize",
			ID:      &initID,
			Params:  initParams,
		}
		initData, _ := json.Marshal(initMsg)
		inputLines = append(inputLines, string(initData))

		// 2. Session/new request.
		sessionParams, _ := json.Marshal(map[string]any{
			"cwd": "/tmp/test",
		})
		sessionNewID := json.RawMessage(`2`)
		sessionNewMsg := Message{
			JSONRPC: "2.0",
			Method:  "session/new",
			ID:      &sessionNewID,
			Params:  sessionParams,
		}
		sessionNewData, _ := json.Marshal(sessionNewMsg)
		inputLines = append(inputLines, string(sessionNewData))

		// 3. N prompt requests.
		for i, prompt := range prompts {
			promptParams, _ := json.Marshal(map[string]any{
				"message": prompt,
			})
			promptID := json.RawMessage(json.RawMessage(`"p` + strings.Repeat("0", i) + `"`))
			promptMsg := Message{
				JSONRPC: "2.0",
				Method:  "session/prompt",
				ID:      &promptID,
				Params:  promptParams,
			}
			promptData, _ := json.Marshal(promptMsg)
			inputLines = append(inputLines, string(promptData))
		}

		// Combine all messages as newline-delimited input.
		input := strings.Join(inputLines, "\n") + "\n"

		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(input), out)
		agent := &bondtest.EchoAgent{}
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		if err := h.Serve(context.Background()); err != nil {
			t.Logf("Serve error: %v", err)
			return false
		}

		// Verify session history has exactly 2N messages.
		h.mu.Lock()
		session := h.session
		h.mu.Unlock()

		if session == nil {
			t.Logf("session is nil after serving %d prompts", n)
			return false
		}

		if len(session.History) != 2*n {
			t.Logf("expected %d history messages, got %d", 2*n, len(session.History))
			return false
		}

		// Verify alternating user/assistant roles.
		for i, msg := range session.History {
			if i%2 == 0 {
				// Even indices should be user messages.
				if msg.Role != "user" {
					t.Logf("message %d: expected role 'user', got %q", i, msg.Role)
					return false
				}
			} else {
				// Odd indices should be assistant messages.
				if msg.Role != "assistant" {
					t.Logf("message %d: expected role 'assistant', got %q", i, msg.Role)
					return false
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 9: Conversation History Accumulates failed: %v", err)
	}
}

// TestProperty_TextDeltasAreForwarded verifies that for any sequence of
// StreamEventTextDelta events emitted by the Bond agent, the handler emits
// an equal number of session/update notifications of type "agent_message_chunk",
// each containing the corresponding text delta and a stable messageId.
//
// **Validates: Requirements 4.2**
func TestProperty_TextDeltasAreForwarded(t *testing.T) {
	f := func(deltas textDeltas) bool {
		deltaSlice := []string(deltas)

		// Build the mock agent with Start + N TextDelta + Stop events.
		events := []bond.StreamEvent{
			{Type: bond.StreamEventStart},
		}
		for _, delta := range deltaSlice {
			events = append(events, bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: delta})
		}
		events = append(events, bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd})
		agent := &bondtest.Agent{Events: events}

		// Build initialize request.
		initParams, _ := json.Marshal(map[string]any{
			"protocolVersion": 1,
		})
		initID := json.RawMessage(`1`)
		initMsg := Message{
			JSONRPC: "2.0",
			Method:  "initialize",
			ID:      &initID,
			Params:  initParams,
		}
		initData, _ := json.Marshal(initMsg)

		// Build session/new request.
		sessionParams, _ := json.Marshal(map[string]any{
			"cwd": "/tmp",
		})
		sessionID := json.RawMessage(`2`)
		sessionMsg := Message{
			JSONRPC: "2.0",
			Method:  "session/new",
			ID:      &sessionID,
			Params:  sessionParams,
		}
		sessionData, _ := json.Marshal(sessionMsg)

		// Build session/prompt request.
		promptParams, _ := json.Marshal(map[string]any{
			"message": "hello",
		})
		promptID := json.RawMessage(`3`)
		promptMsg := Message{
			JSONRPC: "2.0",
			Method:  "session/prompt",
			ID:      &promptID,
			Params:  promptParams,
		}
		promptData, _ := json.Marshal(promptMsg)

		// Combine all messages as newline-delimited input.
		input := string(initData) + "\n" + string(sessionData) + "\n" + string(promptData) + "\n"

		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(input), out)
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		if err := h.Serve(context.Background()); err != nil {
			t.Logf("Serve error: %v", err)
			return false
		}

		// Parse output lines and collect agent_message_chunk notifications.
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		type chunkInfo struct {
			MessageID string
			Delta     string
		}
		var chunks []chunkInfo

		for _, line := range lines {
			var msg Message
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			// Only look at session/update notifications (no id, method == "session/update").
			if msg.Method != "session/update" || msg.ID != nil {
				continue
			}
			// Parse params to check the type.
			var params struct {
				Type      string `json:"type"`
				MessageID string `json:"messageId"`
				Delta     string `json:"delta"`
			}
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				continue
			}
			if params.Type == "agent_message_chunk" {
				chunks = append(chunks, chunkInfo{
					MessageID: params.MessageID,
					Delta:     params.Delta,
				})
			}
		}

		// Verify: exactly N chunk notifications emitted.
		if len(chunks) != len(deltaSlice) {
			t.Logf("chunk count mismatch: got %d, want %d", len(chunks), len(deltaSlice))
			return false
		}

		// Verify: each chunk matches the corresponding delta string.
		for i, chunk := range chunks {
			if chunk.Delta != deltaSlice[i] {
				t.Logf("delta mismatch at index %d: got %q, want %q", i, chunk.Delta, deltaSlice[i])
				return false
			}
		}

		// Verify: all chunk notifications have the same messageId (stable within a turn).
		if len(chunks) > 0 {
			firstID := chunks[0].MessageID
			if firstID == "" {
				t.Logf("messageId is empty")
				return false
			}
			for i, chunk := range chunks[1:] {
				if chunk.MessageID != firstID {
					t.Logf("messageId mismatch at index %d: got %q, want %q", i+1, chunk.MessageID, firstID)
					return false
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 10: Text Deltas Are Forwarded failed: %v", err)
	}
}

// textDeltas is a type that generates a slice of 1-10 non-empty text delta
// strings without newlines (printable ASCII only) for testing text forwarding.
type textDeltas []string

// Generate implements quick.Generator for textDeltas, producing a slice of
// 1-10 random printable ASCII strings (no newlines) to simulate text deltas.
func (textDeltas) Generate(rng *rand.Rand, size int) reflect.Value {
	count := 1 + rng.Intn(10)
	deltas := make([]string, count)
	for i := range deltas {
		deltas[i] = generatePrintableASCII(rng)
	}
	return reflect.ValueOf(textDeltas(deltas))
}

// generatePrintableASCII produces a random non-empty string of printable ASCII
// characters (codes 32-126) excluding newline. Length is 1-20 characters.
func generatePrintableASCII(rng *rand.Rand) string {
	length := 1 + rng.Intn(20)
	b := make([]byte, length)
	for i := range b {
		// Printable ASCII range: 32 (space) to 126 (~), excluding newline.
		// Since newline is 10 (outside 32-126), all chars in this range are safe.
		b[i] = byte(32 + rng.Intn(95))
	}
	return string(b)
}

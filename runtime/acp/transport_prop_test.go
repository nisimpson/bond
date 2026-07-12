package acp

import (
	"encoding/json"
	"io"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond/provider/acpproxy"
)

// TestProperty_JSONRPCMessageRoundTrip verifies that for any valid JSON-RPC 2.0
// message (request, response, or notification), serializing it via WriteMessage
// into a pipe and reading it back via ReadMessage produces a message equivalent
// to the original after deserialization.
//
// **Validates: Requirements 10.4, 1.1, 1.2**
func TestProperty_JSONRPCMessageRoundTrip(t *testing.T) {
	f := func(msg Message) bool {
		pr, pw := io.Pipe()
		transport := NewTransport(pr, pw)

		// Write in a goroutine since pipe is synchronous.
		errCh := make(chan error, 1)
		go func() {
			data, merr := json.Marshal(msg)
			if merr != nil {
				errCh <- merr
				pw.Close()
				return
			}
			errCh <- transport.WriteMessage(data)
			pw.Close()
		}()

		// Read back.
		raw, err := transport.ReadMessage()
		if err != nil {
			t.Logf("ReadMessage error: %v", err)
			return false
		}

		if writeErr := <-errCh; writeErr != nil {
			t.Logf("WriteMessage error: %v", writeErr)
			return false
		}

		// Deserialize back into a Message.
		var got Message
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Logf("Unmarshal error: %v", err)
			return false
		}

		// Compare fields for equivalence.
		if got.JSONRPC != msg.JSONRPC {
			t.Logf("JSONRPC mismatch: got %q, want %q", got.JSONRPC, msg.JSONRPC)
			return false
		}
		if got.Method != msg.Method {
			t.Logf("Method mismatch: got %q, want %q", got.Method, msg.Method)
			return false
		}
		if !rawMessageEqual(got.ID, msg.ID) {
			t.Logf("ID mismatch: got %s, want %s", safeRawString(got.ID), safeRawString(msg.ID))
			return false
		}
		if !rawEqual(got.Params, msg.Params) {
			t.Logf("Params mismatch: got %s, want %s", string(got.Params), string(msg.Params))
			return false
		}
		if !rawEqual(got.Result, msg.Result) {
			t.Logf("Result mismatch: got %s, want %s", string(got.Result), string(msg.Result))
			return false
		}
		if !errorObjectEqual(got.Error, msg.Error) {
			t.Logf("Error mismatch")
			return false
		}
		return true
	}

	cfg := &quick.Config{
		MaxCount: 200,
		Values: func(values []reflect.Value, rnd *rand.Rand) {
			values[0] = reflect.ValueOf(generateMessage(rnd))
		},
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Errorf("JSON-RPC message round-trip property failed: %v", err)
	}
}

// generateMessage produces a valid JSON-RPC 2.0 message (request, response, or notification).
func generateMessage(rand *rand.Rand) Message {
	msg := Message{JSONRPC: "2.0"}

	// Randomly choose message type: 0=request, 1=response, 2=notification
	switch rand.Intn(3) {
	case 0:
		// Request: has id and method, optional params
		msg.ID = generateID(rand)
		msg.Method = generateMethod(rand)
		if rand.Intn(2) == 0 {
			msg.Params = generateParams(rand)
		}
	case 1:
		// Response: has id and either result or error
		msg.ID = generateID(rand)
		if rand.Intn(2) == 0 {
			msg.Result = generateResult(rand)
		} else {
			msg.Error = generateError(rand)
		}
	case 2:
		// Notification: has method, optional params, no id
		msg.Method = generateMethod(rand)
		if rand.Intn(2) == 0 {
			msg.Params = generateParams(rand)
		}
	}

	return msg
}

// generateID produces a random JSON-RPC id (string or integer).
func generateID(rand *rand.Rand) *json.RawMessage {
	var raw json.RawMessage
	if rand.Intn(2) == 0 {
		// Integer id
		id := rand.Intn(10000)
		raw, _ = json.Marshal(id)
	} else {
		// String id
		id := generateAlphanumeric(rand, 1+rand.Intn(12))
		raw, _ = json.Marshal(id)
	}
	return &raw
}

// generateMethod produces a random method name like "session/prompt" or "initialize".
func generateMethod(rand *rand.Rand) string {
	methods := []string{
		"initialize",
		"session/new",
		"session/prompt",
		"session/cancel",
		"tools/list",
		"custom/method",
	}
	return methods[rand.Intn(len(methods))]
}

// generateParams produces a random JSON object for params.
func generateParams(rand *rand.Rand) json.RawMessage {
	params := map[string]any{}
	n := rand.Intn(4)
	for i := 0; i < n; i++ {
		key := generateAlphanumeric(rand, 3+rand.Intn(8))
		params[key] = generateSimpleValue(rand)
	}
	raw, _ := json.Marshal(params)
	return raw
}

// generateResult produces a random JSON value for result.
func generateResult(rand *rand.Rand) json.RawMessage {
	result := map[string]any{
		"status": generateAlphanumeric(rand, 4+rand.Intn(6)),
	}
	raw, _ := json.Marshal(result)
	return raw
}

// generateError produces a random ErrorObject.
func generateError(rand *rand.Rand) *ErrorObject {
	codes := []int{acpproxy.CodeParseError, acpproxy.CodeInvalidRequest, acpproxy.CodeMethodNotFound, acpproxy.CodeInvalidParams, acpproxy.CodeServerNotInit, acpproxy.CodeNoActiveSession}
	errObj := &ErrorObject{
		Code:    codes[rand.Intn(len(codes))],
		Message: generateAlphanumeric(rand, 5+rand.Intn(20)),
	}
	if rand.Intn(2) == 0 {
		data, _ := json.Marshal(map[string]string{"detail": generateAlphanumeric(rand, 5)})
		errObj.Data = data
	}
	return errObj
}

// generateSimpleValue produces a random JSON-compatible simple value.
func generateSimpleValue(rand *rand.Rand) any {
	switch rand.Intn(4) {
	case 0:
		return rand.Intn(1000)
	case 1:
		return generateAlphanumeric(rand, 3+rand.Intn(10))
	case 2:
		return rand.Intn(2) == 0
	default:
		return nil
	}
}

// generateAlphanumeric produces a random alphanumeric string of the given length.
func generateAlphanumeric(rand *rand.Rand, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// rawMessageEqual compares two *json.RawMessage pointers for JSON equivalence.
func rawMessageEqual(a, b *json.RawMessage) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return rawEqual(*a, *b)
}

// rawEqual compares two json.RawMessage values for JSON equivalence.
func rawEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// errorObjectEqual compares two *ErrorObject for equivalence.
func errorObjectEqual(a, b *ErrorObject) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Code != b.Code || a.Message != b.Message {
		return false
	}
	return rawEqual(a.Data, b.Data)
}

// safeRawString returns a string representation of a *json.RawMessage for logging.
func safeRawString(r *json.RawMessage) string {
	if r == nil {
		return "<nil>"
	}
	return string(*r)
}

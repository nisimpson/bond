package agentacp_test

import (
	"encoding/json"
	"io"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/nisimpson/bond/agent/agentacp"
	"github.com/nisimpson/bond/agent/agentacp/acpio"
)

// TestProperty_JSONRPCMessageRoundTrip verifies that for any valid JSON-RPC 2.0
// message (request, response, or notification), serializing it via WriteMessage
// into a pipe and reading it back via ReadMessage produces a message equivalent
// to the original after deserialization.
//
// Feature: acp-proxy, Property 1: JSON-RPC Message Round-Trip
// **Validates: Requirements 12.5**
func TestProperty_JSONRPCMessageRoundTrip(t *testing.T) {
	f := func(msg agentacp.Message) bool {
		pr, pw := io.Pipe()
		transport := acpio.NewTransport(pr, pw)

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
		var got agentacp.Message
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
		if !rawPtrEqual(got.ID, msg.ID) {
			t.Logf("ID mismatch: got %s, want %s", safeRawStr(got.ID), safeRawStr(msg.ID))
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
		if !errObjEqual(got.Error, msg.Error) {
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

// --- Generators ---

// generateMessage produces a valid JSON-RPC 2.0 message (request, response, or notification).
func generateMessage(rnd *rand.Rand) agentacp.Message {
	msg := agentacp.Message{JSONRPC: "2.0"}

	// Randomly choose message type: 0=request, 1=response, 2=notification
	switch rnd.Intn(3) {
	case 0:
		// Request: has id and method, optional params
		msg.ID = generateID(rnd)
		msg.Method = generateMethod(rnd)
		if rnd.Intn(2) == 0 {
			msg.Params = generateParams(rnd)
		}
	case 1:
		// Response: has id and either result or error
		msg.ID = generateID(rnd)
		if rnd.Intn(2) == 0 {
			msg.Result = generateResult(rnd)
		} else {
			msg.Error = generateError(rnd)
		}
	case 2:
		// Notification: has method, optional params, no id
		msg.Method = generateMethod(rnd)
		if rnd.Intn(2) == 0 {
			msg.Params = generateParams(rnd)
		}
	}

	return msg
}

// generateID produces a random JSON-RPC id (string or integer).
func generateID(rnd *rand.Rand) *json.RawMessage {
	var raw json.RawMessage
	if rnd.Intn(2) == 0 {
		// Integer id
		id := rnd.Intn(10000)
		raw, _ = json.Marshal(id)
	} else {
		// String id
		id := generateAlphanumeric(rnd, 1+rnd.Intn(12))
		raw, _ = json.Marshal(id)
	}
	return &raw
}

// generateMethod produces a random method name.
func generateMethod(rnd *rand.Rand) string {
	methods := []string{
		"initialize",
		"session/new",
		"session/prompt",
		"session/cancel",
		"session/update",
		"session/request_permission",
		"tools/list",
		"custom/method",
	}
	return methods[rnd.Intn(len(methods))]
}

// generateParams produces a random JSON object for params.
func generateParams(rnd *rand.Rand) json.RawMessage {
	params := map[string]any{}
	n := rnd.Intn(4)
	for i := 0; i < n; i++ {
		key := generateAlphanumeric(rnd, 3+rnd.Intn(8))
		params[key] = generateSimpleValue(rnd)
	}
	raw, _ := json.Marshal(params)
	return raw
}

// generateResult produces a random JSON value for result.
func generateResult(rnd *rand.Rand) json.RawMessage {
	result := map[string]any{
		"status": generateAlphanumeric(rnd, 4+rnd.Intn(6)),
	}
	raw, _ := json.Marshal(result)
	return raw
}

// generateError produces a random agentacp.ErrorObject.
func generateError(rnd *rand.Rand) *agentacp.ErrorObject {
	codes := []int{
		agentacp.CodeParseError, agentacp.CodeInvalidRequest, agentacp.CodeMethodNotFound,
		agentacp.CodeInvalidParams, agentacp.CodeInternalError, agentacp.CodeServerNotInit,
		agentacp.CodeNoActiveSession,
	}
	errObj := &agentacp.ErrorObject{
		Code:    codes[rnd.Intn(len(codes))],
		Message: generateAlphanumeric(rnd, 5+rnd.Intn(20)),
	}
	if rnd.Intn(2) == 0 {
		data, _ := json.Marshal(map[string]string{"detail": generateAlphanumeric(rnd, 5)})
		errObj.Data = data
	}
	return errObj
}

// generateSimpleValue produces a random JSON-compatible simple value.
func generateSimpleValue(rnd *rand.Rand) any {
	switch rnd.Intn(4) {
	case 0:
		return rnd.Intn(1000)
	case 1:
		return generateAlphanumeric(rnd, 3+rnd.Intn(10))
	case 2:
		return rnd.Intn(2) == 0
	default:
		return nil
	}
}

// generateAlphanumeric produces a random alphanumeric string of the given length.
func generateAlphanumeric(rnd *rand.Rand, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rnd.Intn(len(chars))]
	}
	return string(b)
}

// --- Comparison helpers ---

// rawPtrEqual compares two *json.RawMessage pointers for JSON equivalence.
func rawPtrEqual(a, b *json.RawMessage) bool {
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

// errObjEqual compares two *agentacp.ErrorObject for equivalence.
func errObjEqual(a, b *agentacp.ErrorObject) bool {
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

// safeRawStr returns a string representation of a *json.RawMessage for logging.
func safeRawStr(r *json.RawMessage) string {
	if r == nil {
		return "<nil>"
	}
	return string(*r)
}

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

	"github.com/nisimpson/bond/agent/agentacp"
	"github.com/nisimpson/bond/bondtest"
)

// TestProperty_MalformedMessagesYieldParseError verifies that for any byte
// sequence that is not valid JSON, or any JSON object missing the required
// `jsonrpc` field set to "2.0" or missing the `method` field, the handler
// responds with a JSON-RPC error containing code -32700.
//
// **Validates: Requirements 1.4**
func TestProperty_MalformedMessagesYieldParseError(t *testing.T) {
	f := func(input malformedInput) bool {
		var out bytes.Buffer
		transport := NewTransport(strings.NewReader(input.Line+"\n"), &out)
		agent := &bondtest.EchoAgent{}
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		_ = h.Serve(context.Background())

		// Parse the response from the output buffer.
		respBytes := bytes.TrimSpace(out.Bytes())
		if len(respBytes) == 0 {
			t.Logf("no response for input: %q", input.Line)
			return false
		}

		var resp Message
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			t.Logf("failed to parse response: %v (raw: %q)", err, string(respBytes))
			return false
		}

		if resp.Error == nil {
			t.Logf("expected error response, got none for input: %q", input.Line)
			return false
		}

		if resp.Error.Code != agentacp.CodeParseError {
			t.Logf("expected error code %d, got %d for input: %q", agentacp.CodeParseError, resp.Error.Code, input.Line)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 4 (Malformed Messages Yield Parse Error) failed: %v", err)
	}
}

// malformedInput represents a generated malformed JSON-RPC message line.
// It covers three categories:
//  1. Invalid JSON (random bytes that aren't valid JSON)
//  2. Valid JSON but missing `jsonrpc` field (or not "2.0")
//  3. Valid JSON with `jsonrpc: "2.0"` but missing `method` field
type malformedInput struct {
	Line string
}

// Generate implements quick.Generator for malformedInput.
func (malformedInput) Generate(r *rand.Rand, size int) reflect.Value {
	var line string

	switch r.Intn(3) {
	case 0:
		// Category 1: Invalid JSON (random bytes that can't parse as JSON)
		line = generateInvalidJSON(r)
	case 1:
		// Category 2: Valid JSON but missing or wrong `jsonrpc` field
		line = generateMissingJSONRPC(r)
	case 2:
		// Category 3: Valid JSON with `jsonrpc: "2.0"` but missing `method` field
		line = generateMissingMethod(r)
	}

	return reflect.ValueOf(malformedInput{Line: line})
}

// generateInvalidJSON produces a random byte sequence that is not valid JSON.
func generateInvalidJSON(r *rand.Rand) string {
	length := 1 + r.Intn(50)
	b := make([]byte, length)
	for i := range b {
		chars := "abcdefghijklmnopqrstuvwxyz!@#$%^&*<>?;:'"
		b[i] = chars[r.Intn(len(chars))]
	}
	result := string(b)
	// Ensure it's actually invalid JSON; if by some miracle it's valid, prefix with garbage.
	if json.Valid([]byte(result)) {
		result = "{invalid" + result
	}
	return result
}

// generateMissingJSONRPC produces valid JSON but with a missing or incorrect
// `jsonrpc` field.
func generateMissingJSONRPC(r *rand.Rand) string {
	obj := map[string]any{
		"id":     r.Intn(1000),
		"method": "initialize",
	}

	switch r.Intn(3) {
	case 0:
		// No jsonrpc field at all (already missing from map)
	case 1:
		// Wrong version string
		versions := []string{"1.0", "2.1", "3.0", "", "1", "jsonrpc"}
		obj["jsonrpc"] = versions[r.Intn(len(versions))]
	case 2:
		// Non-string jsonrpc value
		obj["jsonrpc"] = r.Intn(100)
	}

	data, _ := json.Marshal(obj)
	return string(data)
}

// generateMissingMethod produces valid JSON with `jsonrpc: "2.0"` but missing
// the `method` field.
func generateMissingMethod(r *rand.Rand) string {
	obj := map[string]any{
		"jsonrpc": "2.0",
		"id":      r.Intn(1000),
	}

	// Optionally add params or other fields, but never `method`.
	if r.Intn(2) == 0 {
		obj["params"] = map[string]any{"key": "value"}
	}

	data, _ := json.Marshal(obj)
	return string(data)
}

// notification is a test type representing a valid JSON-RPC 2.0 notification
// (no id field). Used as input to the property test generator.
type notification struct {
	Method string
	Params json.RawMessage
}

// Generate implements quick.Generator for notification, producing valid
// JSON-RPC 2.0 notifications with known and random method names, and optional params.
func (notification) Generate(rng *rand.Rand, size int) reflect.Value {
	n := notification{}

	// Mix of known ACP methods and random method strings.
	knownMethods := []string{
		"initialize",
		"session/new",
		"session/prompt",
		"session/cancel",
	}
	if rng.Intn(2) == 0 {
		// Use a known method.
		n.Method = knownMethods[rng.Intn(len(knownMethods))]
	} else {
		// Use a random method string.
		n.Method = generateHandlerPropMethod(rng)
	}

	// Optionally include params.
	if rng.Intn(2) == 0 {
		params := map[string]any{}
		numKeys := rng.Intn(4)
		for i := 0; i < numKeys; i++ {
			key := generateHandlerPropString(rng, 3+rng.Intn(8))
			params[key] = generateHandlerPropValue(rng)
		}
		raw, _ := json.Marshal(params)
		n.Params = raw
	}

	return reflect.ValueOf(n)
}

// toJSON serializes the notification into a compact JSON line (no id field).
func (n notification) toJSON() string {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  n.Method,
	}
	if len(n.Params) > 0 {
		msg["params"] = json.RawMessage(n.Params)
	}
	data, _ := json.Marshal(msg)
	return string(data)
}

// TestProperty_NotificationsProduceNoResponse verifies that for any valid
// JSON-RPC 2.0 notification (a message without an `id` field), sending it to
// the handler results in zero bytes written to the response writer.
//
// **Validates: Requirements 1.6, 10.3**
func TestProperty_NotificationsProduceNoResponse(t *testing.T) {
	f := func(n notification) bool {
		// Build the notification as a JSON line.
		line := n.toJSON() + "\n"

		// Create a handler with a bytes.Buffer output.
		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(line), out)
		agent := &bondtest.EchoAgent{}
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		// Serve processes the message and returns on EOF.
		err := h.Serve(context.Background())
		if err != nil {
			t.Logf("Serve returned error: %v", err)
			return false
		}

		// The key property: no bytes written for a notification.
		if out.Len() != 0 {
			t.Logf("expected zero bytes for notification %q, got %d bytes: %s",
				n.Method, out.Len(), out.String())
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 2: Notifications Produce No Response failed: %v", err)
	}
}

// TestProperty_ResponseIDEchoesRequestID verifies that for any valid JSON-RPC
// request with an `id` field, the handler's response contains an `id` field
// with the same value as the request's `id`.
//
// **Validates: Requirements 1.3**
func TestProperty_ResponseIDEchoesRequestID(t *testing.T) {
	f := func(id requestID) bool {
		idRaw := json.RawMessage(id.Raw)

		// Build a valid initialize request with the generated ID.
		params, _ := json.Marshal(map[string]any{
			"protocolVersion": 1,
		})
		msg := Message{
			JSONRPC: "2.0",
			Method:  "initialize",
			ID:      &idRaw,
			Params:  params,
		}
		data, _ := json.Marshal(msg)
		input := string(data) + "\n"

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

		// Parse the response.
		var resp Message
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
			t.Logf("failed to parse response: %v", err)
			return false
		}

		// Verify response has an ID.
		if resp.ID == nil {
			t.Logf("response ID is nil for request ID %s", id.Raw)
			return false
		}

		// Verify the response ID matches the request ID exactly.
		if string(*resp.ID) != string(idRaw) {
			t.Logf("response ID %s does not match request ID %s", string(*resp.ID), id.Raw)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 3: Response ID Echoes Request ID failed: %v", err)
	}
}

// requestID generates arbitrary JSON-RPC id values (string or integer).
type requestID struct {
	Raw string // raw JSON representation of the ID
}

// Generate implements quick.Generator for requestID, producing random integer
// and string ID values.
func (requestID) Generate(rng *rand.Rand, size int) reflect.Value {
	var raw string
	if rng.Intn(2) == 0 {
		// Integer ID (0 to 99999)
		id := rng.Intn(100000)
		data, _ := json.Marshal(id)
		raw = string(data)
	} else {
		// String ID (alphanumeric, 1-16 chars)
		length := 1 + rng.Intn(16)
		const chars = "abcdefghijklmnopqrstuvwxyz0123456789-_"
		b := make([]byte, length)
		for i := range b {
			b[i] = chars[rng.Intn(len(chars))]
		}
		data, _ := json.Marshal(string(b))
		raw = string(data)
	}
	return reflect.ValueOf(requestID{Raw: raw})
}

// TestProperty_UnknownMethodsYieldMethodNotFound verifies that for any method
// string NOT in the registered ACP method set, sending a JSON-RPC request with
// that method to the handler produces an error response with code -32601.
//
// **Validates: Requirements 1.5**
func TestProperty_UnknownMethodsYieldMethodNotFound(t *testing.T) {
	// registeredMethods is the set of methods known to the handler.
	registeredMethods := map[string]bool{
		"initialize":     true,
		"session/new":    true,
		"session/prompt": true,
		"session/cancel": true,
	}

	f := func(method unknownMethod) bool {
		methodStr := string(method)

		// Skip if it happens to be a registered method (shouldn't happen due to generator).
		if registeredMethods[methodStr] {
			return true
		}

		// Build a valid JSON-RPC request with an id and the unknown method.
		id := json.RawMessage(`1`)
		msg := Message{
			JSONRPC: "2.0",
			Method:  methodStr,
			ID:      &id,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			t.Logf("failed to marshal request: %v", err)
			return false
		}

		input := string(data) + "\n"
		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(input), out)
		agent := &bondtest.EchoAgent{}
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		// Initialize the handler first so the pre-initialization guard doesn't
		// intercept the request before method dispatch.
		h.mu.Lock()
		h.initialized = true
		h.mu.Unlock()

		if err := h.Serve(context.Background()); err != nil {
			t.Logf("Serve error: %v", err)
			return false
		}

		// Parse the response.
		var resp Message
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
			t.Logf("failed to parse response for method %q: %v", methodStr, err)
			return false
		}

		// Verify error code is -32601.
		if resp.Error == nil {
			t.Logf("expected error response for method %q, got result", methodStr)
			return false
		}
		if resp.Error.Code != agentacp.CodeMethodNotFound {
			t.Logf("expected error code %d for method %q, got %d", agentacp.CodeMethodNotFound, methodStr, resp.Error.Code)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 5 (Unknown Methods Yield Method Not Found) failed: %v", err)
	}
}

// unknownMethod is a type that generates method strings NOT in the registered set.
type unknownMethod string

// Generate implements quick.Generator for unknownMethod, producing random method
// strings that are NOT in {"initialize", "session/new", "session/prompt", "session/cancel"}.
func (unknownMethod) Generate(rng *rand.Rand, size int) reflect.Value {
	registeredMethods := map[string]bool{
		"initialize":     true,
		"session/new":    true,
		"session/prompt": true,
		"session/cancel": true,
	}

	// Strategy: generate random method-like strings and reject registered ones.
	// Use a mix of prefixes and suffixes to generate realistic method names.
	prefixes := []string{
		"tools", "workspace", "completion", "debug", "custom",
		"session", "agent", "config", "system", "unknown",
		"foo", "bar", "rpc", "test", "x",
	}
	suffixes := []string{
		"list", "get", "set", "delete", "update", "create",
		"start", "stop", "run", "cancel", "new", "close",
		"prompt", "init", "exec", "query",
	}

	for {
		var method string
		switch rng.Intn(3) {
		case 0:
			// prefix/suffix style
			method = prefixes[rng.Intn(len(prefixes))] + "/" + suffixes[rng.Intn(len(suffixes))]
		case 1:
			// single word style
			method = generateAlphanumeric(rng, 3+rng.Intn(12))
		case 2:
			// dotted style
			method = prefixes[rng.Intn(len(prefixes))] + "." + suffixes[rng.Intn(len(suffixes))]
		}

		if !registeredMethods[method] {
			return reflect.ValueOf(unknownMethod(method))
		}
		// Extremely rare to collide, but loop if we do.
	}
}

// --- Shared helpers for handler property tests ---

// generateHandlerPropMethod produces a random method-like string (may include slashes).
func generateHandlerPropMethod(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz"
	length := 3 + rng.Intn(10)
	b := make([]byte, length)
	for i := range b {
		if rng.Intn(5) == 0 {
			b[i] = '/'
		} else {
			b[i] = chars[rng.Intn(len(chars))]
		}
	}
	// Ensure the method starts with a letter.
	if b[0] == '/' {
		b[0] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

// generateHandlerPropString produces a random alphanumeric string of the given length.
func generateHandlerPropString(rng *rand.Rand, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

// generateHandlerPropValue produces a random JSON-compatible simple value.
func generateHandlerPropValue(rng *rand.Rand) any {
	switch rng.Intn(4) {
	case 0:
		return rng.Intn(1000)
	case 1:
		return generateHandlerPropString(rng, 3+rng.Intn(10))
	case 2:
		return rng.Intn(2) == 0
	default:
		return nil
	}
}

// TestProperty_UnsupportedProtocolVersionsAreRejected verifies that for any
// integer value other than 1 sent as `protocolVersion` in an `initialize`
// request, the handler responds with a JSON-RPC error with code -32602.
//
// **Validates: Requirements 2.3**
func TestProperty_UnsupportedProtocolVersionsAreRejected(t *testing.T) {
	f := func(version unsupportedVersion) bool {
		v := int(version)

		// Build an initialize request with the unsupported version.
		params, _ := json.Marshal(map[string]any{
			"protocolVersion": v,
		})
		id := json.RawMessage(`1`)
		msg := Message{
			JSONRPC: "2.0",
			Method:  "initialize",
			ID:      &id,
			Params:  params,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			t.Logf("failed to marshal request: %v", err)
			return false
		}

		input := string(data) + "\n"
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

		// Parse the response.
		respBytes := bytes.TrimSpace(out.Bytes())
		if len(respBytes) == 0 {
			t.Logf("no response for protocolVersion %d", v)
			return false
		}

		var resp Message
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			t.Logf("failed to parse response: %v", err)
			return false
		}

		// Verify error response with code -32602.
		if resp.Error == nil {
			t.Logf("expected error response for protocolVersion %d, got result", v)
			return false
		}
		if resp.Error.Code != agentacp.CodeInvalidParams {
			t.Logf("expected error code %d for protocolVersion %d, got %d",
				agentacp.CodeInvalidParams, v, resp.Error.Code)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Feature: acp-handler, Property 6: Unsupported Protocol Versions Are Rejected failed: %v", err)
	}
}

// unsupportedVersion is a type that generates integer values other than 1
// for use as protocolVersion in initialize requests.
type unsupportedVersion int

// Generate implements quick.Generator for unsupportedVersion, producing random
// integers (positive, negative, zero) that are NOT equal to 1.
func (unsupportedVersion) Generate(rng *rand.Rand, size int) reflect.Value {
	for {
		// Generate a random integer in a range that includes negative, zero, and
		// large positive values.
		var v int
		switch rng.Intn(4) {
		case 0:
			// Negative values
			v = -(1 + rng.Intn(1000))
		case 1:
			// Zero
			v = 0
		case 2:
			// Large positive values (> 1)
			v = 2 + rng.Intn(1000)
		case 3:
			// Small positive/negative range near 1 but not 1
			candidates := []int{-1, 0, 2, 3, -2, -3, 100, -100}
			v = candidates[rng.Intn(len(candidates))]
		}

		if v != 1 {
			return reflect.ValueOf(unsupportedVersion(v))
		}
	}
}

// preInitMethod is a type that generates method names excluding "initialize".
// Used to test that all non-initialize methods are rejected before initialization.
type preInitMethod string

// Generate implements quick.Generator for preInitMethod, producing method strings
// that are NOT "initialize". Includes both known ACP methods and random strings.
func (preInitMethod) Generate(rng *rand.Rand, size int) reflect.Value {
	// Mix of known ACP methods (except initialize) and random method strings.
	knownNonInitMethods := []string{
		"session/new",
		"session/prompt",
		"session/cancel",
	}

	var method string
	switch rng.Intn(3) {
	case 0:
		// Use a known non-initialize method.
		method = knownNonInitMethods[rng.Intn(len(knownNonInitMethods))]
	case 1:
		// Use a random method string (prefix/suffix style).
		prefixes := []string{"tools", "workspace", "completion", "debug", "custom", "session", "agent"}
		suffixes := []string{"list", "get", "set", "delete", "create", "start", "stop", "run"}
		method = prefixes[rng.Intn(len(prefixes))] + "/" + suffixes[rng.Intn(len(suffixes))]
	case 2:
		// Use a random alphanumeric string.
		method = generateAlphanumeric(rng, 3+rng.Intn(12))
	}

	// Guard: ensure we never produce "initialize".
	if method == "initialize" {
		method = "not_initialize"
	}

	return reflect.ValueOf(preInitMethod(method))
}

// TestProperty_PreInitializationRejection verifies that for any method name
// other than "initialize", sending a request before the handler has been
// initialized produces a JSON-RPC error response with code -32002.
//
// **Validates: Requirements 2.4**
func TestProperty_PreInitializationRejection(t *testing.T) {
	f := func(method preInitMethod) bool {
		methodStr := string(method)

		// Build a valid JSON-RPC request with an id and the method.
		id := json.RawMessage(`1`)
		msg := Message{
			JSONRPC: "2.0",
			Method:  methodStr,
			ID:      &id,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			t.Logf("failed to marshal request: %v", err)
			return false
		}

		input := string(data) + "\n"
		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(input), out)
		agent := &bondtest.EchoAgent{}
		// Create a FRESH handler — NOT initialized.
		h := NewHandler(agent, Options{
			AgentName:    "test-agent",
			AgentVersion: "1.0.0",
			Transport:    transport,
		})

		if err := h.Serve(context.Background()); err != nil {
			t.Logf("Serve error: %v", err)
			return false
		}

		// Parse the response.
		respBytes := bytes.TrimSpace(out.Bytes())
		if len(respBytes) == 0 {
			t.Logf("no response for method %q", methodStr)
			return false
		}

		var resp Message
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			t.Logf("failed to parse response for method %q: %v", methodStr, err)
			return false
		}

		// Verify error code is -32002 (agentacp.CodeServerNotInit).
		if resp.Error == nil {
			t.Logf("expected error response for method %q, got result", methodStr)
			return false
		}
		if resp.Error.Code != agentacp.CodeServerNotInit {
			t.Logf("expected error code %d for method %q, got %d", agentacp.CodeServerNotInit, methodStr, resp.Error.Code)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 7 (Pre-Initialization Rejection) failed: %v", err)
	}
}

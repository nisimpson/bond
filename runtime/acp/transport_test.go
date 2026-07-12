package acp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestNewTransport(t *testing.T) {
	r := strings.NewReader("")
	w := &bytes.Buffer{}
	tr := NewTransport(r, w)
	if tr == nil {
		t.Fatal("NewTransport returned nil")
	}
	if tr.reader == nil {
		t.Fatal("reader is nil")
	}
	if tr.writer == nil {
		t.Fatal("writer is nil")
	}
}

func TestReadMessage_SingleLine(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":1}` + "\n"
	tr := NewTransport(strings.NewReader(input), io.Discard)

	msg, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed Message
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}
	if parsed.Method != "initialize" {
		t.Errorf("expected method 'initialize', got %q", parsed.Method)
	}
}

func TestReadMessage_MultipleLines(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"a","id":1}` + "\n" +
		`{"jsonrpc":"2.0","method":"b","id":2}` + "\n"
	tr := NewTransport(strings.NewReader(input), io.Discard)

	msg1, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("unexpected error reading first message: %v", err)
	}
	msg2, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("unexpected error reading second message: %v", err)
	}

	var m1, m2 Message
	_ = json.Unmarshal(msg1, &m1)
	_ = json.Unmarshal(msg2, &m2)

	if m1.Method != "a" {
		t.Errorf("first message: expected method 'a', got %q", m1.Method)
	}
	if m2.Method != "b" {
		t.Errorf("second message: expected method 'b', got %q", m2.Method)
	}
}

func TestReadMessage_EOF(t *testing.T) {
	tr := NewTransport(strings.NewReader(""), io.Discard)

	_, err := tr.ReadMessage()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestReadMessage_EOFAfterMessages(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"a","id":1}` + "\n"
	tr := NewTransport(strings.NewReader(input), io.Discard)

	// First read succeeds.
	_, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second read returns EOF.
	_, err = tr.ReadMessage()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestWriteMessage(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTransport(strings.NewReader(""), &buf)

	msg := Message{
		JSONRPC: "2.0",
		Method:  "test",
	}

	if err := tr.WriteMessage(msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("output does not end with newline")
	}

	// Verify it's valid JSON.
	var parsed Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if parsed.Method != "test" {
		t.Errorf("expected method 'test', got %q", parsed.Method)
	}
}

func TestWriteMessage_CompactJSON(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTransport(strings.NewReader(""), &buf)

	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  "foo",
		"params":  map[string]any{"key": "value"},
	}

	if err := tr.WriteMessage(msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	// Compact JSON should not contain indentation newlines.
	if strings.Contains(output, "\n") {
		t.Error("JSON output contains internal newlines (not compact)")
	}
}

func TestRoundTrip(t *testing.T) {
	// Write a message via one transport, read it back via another.
	pr, pw := io.Pipe()

	writer := NewTransport(strings.NewReader(""), pw)
	reader := NewTransport(pr, io.Discard)

	original := Message{
		JSONRPC: "2.0",
		Method:  "session/prompt",
	}

	// Write in a goroutine to avoid blocking.
	go func() {
		_ = writer.WriteMessage(original)
		pw.Close()
	}()

	raw, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if got.JSONRPC != original.JSONRPC {
		t.Errorf("JSONRPC: got %q, want %q", got.JSONRPC, original.JSONRPC)
	}
	if got.Method != original.Method {
		t.Errorf("Method: got %q, want %q", got.Method, original.Method)
	}
}

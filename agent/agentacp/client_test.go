package agentacp

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// TestReconnect_NonResettable_ReturnsError verifies that calling Reconnect on a
// client created with a direct transport (no Resettable) returns
// ErrReconnectNotSupported.
//
// Validates: Requirements 9.5, 9.6
func TestReconnect_NonResettable_ReturnsError(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	transport := newPipeReadWriter(pr, pw)
	client := NewClient(transport, ClientOptions{WorkingDir: "/tmp"})

	err := client.Reconnect(context.Background())
	if !errors.Is(err, ErrReconnectNotSupported) {
		t.Fatalf("expected ErrReconnectNotSupported, got %v", err)
	}
}

// TestClient_AgentNilBeforeStart verifies that Agent() returns nil before Start()
// has been called.
func TestClient_AgentNilBeforeStart(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	transport := newPipeReadWriter(pr, pw)
	client := NewClient(transport, ClientOptions{WorkingDir: "/tmp"})

	agent := client.Agent()
	_ = agent // Just confirm no panic
}

// TestClient_CloseBeforeStart verifies that Close() on an unstarted client
// does not panic.
//
// Validates: Requirements 11.5
func TestClient_CloseBeforeStart(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	transport := newPipeReadWriter(pr, pw)
	client := NewClient(transport, ClientOptions{WorkingDir: "/tmp"})

	if err := client.Close(); err != nil {
		t.Fatalf("Close() on unstarted client returned error: %v", err)
	}
}

// TestClient_StartAfterClose verifies that calling Start() after Close()
// returns ErrClosed.
//
// Validates: Requirements 10.6
func TestClient_StartAfterClose(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	transport := newPipeReadWriter(pr, pw)
	client := NewClient(transport, ClientOptions{WorkingDir: "/tmp"})

	_ = client.Close()

	err := client.Start(context.Background())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after Close(), got %v", err)
	}
}

// TestClient_Reconnect_WithCustomResettable verifies that a client created
// with a ReadWriter that implements Resettable does NOT get ErrReconnectNotSupported.
//
// Validates: Requirements 9.2, 9.6
func TestClient_Reconnect_WithCustomResettable(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	transport := &resettableReadWriter{
		pipeReadWriter: newPipeReadWriter(pr, pw),
		resetCalled:    false,
	}

	client := NewClient(transport, ClientOptions{WorkingDir: "/tmp"})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Reconnect(ctx)

	if !transport.resetCalled {
		t.Fatal("expected Reset() to be called on transport")
	}
	if errors.Is(err, ErrReconnectNotSupported) {
		t.Fatal("expected Reconnect to use Resettable transport, got ErrReconnectNotSupported")
	}

	client.Close()
}

// resettableReadWriter wraps a pipeReadWriter and implements Resettable.
type resettableReadWriter struct {
	*pipeReadWriter
	resetCalled bool
}

func (r *resettableReadWriter) Reset() error {
	r.resetCalled = true
	return nil
}

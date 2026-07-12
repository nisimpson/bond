package acpio

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// TestStdioProcess_StartAndReadWrite verifies that Start spawns a process and
// the StdioProcess itself can be used as a ReadWriter. We use `cat` which
// echoes stdin to stdout.
func TestStdioProcess_StartAndReadWrite(t *testing.T) {
	proc := NewStdioProcess("cat", nil, StdioOptions{})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer proc.Close()

	// Write a JSON message via the StdioProcess (cat will echo it back).
	msg := map[string]string{"hello": "world"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if err := proc.WriteMessage(data); err != nil {
		t.Fatalf("WriteMessage() failed: %v", err)
	}

	// Read the echoed message back.
	raw, err := proc.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() failed: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if got["hello"] != "world" {
		t.Errorf("expected hello=world, got %v", got)
	}
}

// TestStdioProcess_CloseGraceful verifies that closing a process sends EOF
// and waits for exit. cat exits 0 on EOF.
func TestStdioProcess_CloseGraceful(t *testing.T) {
	proc := NewStdioProcess("cat", nil, StdioOptions{})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := proc.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}

	if code := proc.ExitCode(); code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

// TestStdioProcess_CloseTimeout_SIGKILL verifies that a hanging process
// is killed via SIGKILL when the shutdown timeout expires.
func TestStdioProcess_CloseTimeout_SIGKILL(t *testing.T) {
	proc := NewStdioProcess("sleep", []string{"100"}, StdioOptions{
		Timeout: 100 * time.Millisecond,
	})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	start := time.Now()
	if err := proc.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	elapsed := time.Since(start)

	// Close should complete in roughly the timeout duration, not 100 seconds.
	if elapsed > 2*time.Second {
		t.Errorf("Close() took too long: %v (expected ~100ms)", elapsed)
	}

	// Process was killed, so exit code should be non-zero.
	if code := proc.ExitCode(); code == 0 {
		t.Errorf("expected non-zero exit code for killed process, got 0")
	}
}

// TestStdioProcess_StderrForwarding verifies that stderr output from the
// subprocess is forwarded to the configured writer.
func TestStdioProcess_StderrForwarding(t *testing.T) {
	var stderrBuf bytes.Buffer
	proc := NewStdioProcess("sh", []string{"-c", "echo diagnostic >&2 && cat"}, StdioOptions{
		Stderr: &stderrBuf,
	})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Give the subprocess a moment to write to stderr.
	time.Sleep(200 * time.Millisecond)

	if err := proc.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}

	output := stderrBuf.String()
	if !strings.Contains(output, "diagnostic") {
		t.Errorf("expected stderr to contain 'diagnostic', got %q", output)
	}
}

// TestStdioProcess_EnvironmentVariables verifies that environment variables
// are passed to the subprocess.
func TestStdioProcess_EnvironmentVariables(t *testing.T) {
	// Use "sh -c" with a script that prints the env var then waits for stdin to close.
	// This prevents the pipe from closing before ReadMessage can read.
	proc := NewStdioProcess("sh", []string{"-c", "echo $TEST_ACP_VAR && cat > /dev/null"}, StdioOptions{
		Env: []string{"TEST_ACP_VAR=hello123"},
	})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer proc.Close()

	// The subprocess prints the env var then blocks on cat. Read the output.
	raw, err := proc.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() failed: %v", err)
	}

	// The output is a raw line "hello123" (not valid JSON, but ReadMessage
	// returns the raw bytes of the line).
	output := string(raw)
	if !strings.Contains(output, "hello123") {
		t.Errorf("expected output to contain 'hello123', got %q", output)
	}
}

// TestStdioProcess_UnexpectedExit verifies that when the process exits
// unexpectedly, subsequent reads return an error and ExitCode is captured.
func TestStdioProcess_UnexpectedExit(t *testing.T) {
	proc := NewStdioProcess("sh", []string{"-c", "exit 42"}, StdioOptions{})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer proc.Close()

	// Wait for the process to exit.
	time.Sleep(200 * time.Millisecond)

	// Reading from the process should return EOF or an error.
	_, err := proc.ReadMessage()
	if err == nil {
		t.Fatal("expected error on read after process exit, got nil")
	}
	if err != io.EOF {
		t.Logf("read error (acceptable): %v", err)
	}

	// Verify exit code is 42.
	if code := proc.ExitCode(); code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}
}

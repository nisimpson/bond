package acpproxy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/acpproxy"
	"github.com/nisimpson/bond/provider/acpproxy/acpio"
)

const mockScript = `
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    method = msg.get("method", "")
    mid = msg.get("id")
    if method == "initialize":
        resp = {"jsonrpc": "2.0", "id": mid, "result": {"protocolVersion": 1, "agentCapabilities": {"promptCapabilities": {"textSupported": True}}, "agentInfo": {"name": "mock", "version": "1.0"}}}
    elif method == "session/new":
        resp = {"jsonrpc": "2.0", "id": mid, "result": {"sessionId": "sess-1"}}
    elif method == "session/prompt":
        resp = {"jsonrpc": "2.0", "id": mid, "result": {"stopReason": "end_turn"}}
    else:
        continue
    sys.stdout.write(json.dumps(resp) + "\n")
    sys.stdout.flush()
`

func newMockClient(t *testing.T, opts acpproxy.ClientOptions, stdioOpts acpio.StdioOptions) *acpproxy.Client {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	proc := acpio.NewStdioProcess("python3", []string{"-c", mockScript}, stdioOpts)
	if err := proc.Start(); err != nil {
		t.Fatalf("StdioProcess.Start() failed: %v", err)
	}
	return acpproxy.NewClient(proc, opts)
}

// TestReconnect_ReperformsInitialization verifies that Reconnect re-performs
// the full initialization sequence and the client remains usable afterward.
//
// Validates: Requirements 9.3, 9.4
func TestReconnect_ReperformsInitialization(t *testing.T) {
	proc := createMockProcess(t)
	client := acpproxy.NewClient(proc, acpproxy.ClientOptions{
		WorkingDir:   "/tmp",
		SystemPrompt: "You are a test agent",
	})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer client.Close()

	if client.AgentInfo().Name != "mock" {
		t.Fatalf("expected agent name 'mock', got %q", client.AgentInfo().Name)
	}

	if err := client.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	if client.AgentInfo().Name != "mock" {
		t.Fatalf("after reconnect, expected agent name 'mock', got %q", client.AgentInfo().Name)
	}

	if agent := client.Agent(); agent == nil {
		t.Fatal("Agent() returned nil after reconnect")
	}
}

// TestConnectionLost_NoAutoReconnect verifies that when the external agent
// process exits, attempting to Stream returns an error rather than auto-reconnecting.
//
// Validates: Requirements 9.5
func TestConnectionLost_NoAutoReconnect(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	exitScript := `
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    method = msg.get("method", "")
    mid = msg.get("id")
    if method == "initialize":
        resp = {"jsonrpc": "2.0", "id": mid, "result": {"protocolVersion": 1, "agentCapabilities": {"promptCapabilities": {"textSupported": True}}, "agentInfo": {"name": "mock", "version": "1.0"}}}
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()
    elif method == "session/new":
        resp = {"jsonrpc": "2.0", "id": mid, "result": {"sessionId": "sess-1"}}
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()
        sys.exit(0)
`
	proc := acpio.NewStdioProcess("python3", []string{"-c", exitScript}, acpio.StdioOptions{Timeout: 2 * time.Second})
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	client := acpproxy.NewClient(proc, acpproxy.ClientOptions{WorkingDir: "/tmp"})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer client.Close()

	time.Sleep(500 * time.Millisecond)

	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
	}

	var gotError bool
	for _, err := range client.Agent().Stream(ctx, messages) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Fatal("expected error when connection is lost, got none")
	}
}

// TestClient_StartTwice verifies that calling Start() twice returns ErrAlreadyStarted.
//
// Validates: Requirements 10.6
func TestClient_StartTwice(t *testing.T) {
	proc := createMockProcess(t)
	client := acpproxy.NewClient(proc, acpproxy.ClientOptions{WorkingDir: "/tmp"})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("first Start() failed: %v", err)
	}
	defer client.Close()

	err := client.Start(ctx)
	if !errors.Is(err, acpproxy.ErrAlreadyStarted) {
		t.Fatalf("expected ErrAlreadyStarted on second Start(), got %v", err)
	}
}

// TestClient_ReconnectAfterClose verifies that Reconnect() after Close() returns ErrClosed.
//
// Validates: Requirements 9.1
func TestClient_ReconnectAfterClose(t *testing.T) {
	proc := createMockProcess(t)
	client := acpproxy.NewClient(proc, acpproxy.ClientOptions{WorkingDir: "/tmp"})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	_ = client.Close()

	err := client.Reconnect(ctx)
	if !errors.Is(err, acpproxy.ErrClosed) {
		t.Fatalf("expected ErrClosed on Reconnect after Close, got %v", err)
	}
}

// TestClient_StreamNoUserMessage verifies that Stream returns an error when
// no user message is present in the messages slice.
//
// Validates: Requirements 4.1
func TestClient_StreamNoUserMessage(t *testing.T) {
	proc := createMockProcess(t)
	client := acpproxy.NewClient(proc, acpproxy.ClientOptions{WorkingDir: "/tmp"})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer client.Close()

	messages := []bond.Message{
		{Role: bond.RoleAssistant, Content: []bond.Block{&bond.TextBlock{Text: "hello"}}},
	}

	var gotError bool
	for _, err := range client.Agent().Stream(ctx, messages) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Fatal("expected error when no user message in slice, got none")
	}
}

// createMockProcess creates and starts a StdioProcess running the mock python script.
func createMockProcess(t *testing.T) *acpio.StdioProcess {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	proc := acpio.NewStdioProcess("python3", []string{"-c", mockScript}, acpio.StdioOptions{Timeout: 5 * time.Second})
	if err := proc.Start(); err != nil {
		t.Fatalf("StdioProcess.Start() failed: %v", err)
	}
	return proc
}

// TestProperty_ReconnectionReplaysPriming verifies that after a successful
// Reconnect(), the client replays the system prompt and initial context in the
// same order as the initial Start().
//
// Feature: acp-proxy, Property 12: Reconnection Replays Priming
// **Validates: Requirements 9.3, 9.4**
func TestProperty_ReconnectionReplaysPriming(t *testing.T) {
	testBin := buildMockServerBinary(t)

	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		systemPrompt := "system-" + randomAlphanumeric(rnd, 8)
		numContext := 1 + rnd.Intn(3)
		initialContext := make([]string, numContext)
		for i := range initialContext {
			initialContext[i] = fmt.Sprintf("ctx-%d-%s", i, randomAlphanumeric(rnd, 6))
		}

		var stderrBuf bytes.Buffer

		proc := acpio.NewStdioProcess(testBin, []string{
			"-test.run=TestHelperMockACPServer",
		}, acpio.StdioOptions{
			Env:     []string{"acpproxy_MOCK_SERVER=1"},
			Stderr:  &stderrBuf,
			Timeout: 5 * time.Second,
		})
		if err := proc.Start(); err != nil {
			t.Logf("Start process failed: %v", err)
			return false
		}

		client := acpproxy.NewClient(proc, acpproxy.ClientOptions{
			WorkingDir:     "/tmp",
			SystemPrompt:   systemPrompt,
			InitialContext: initialContext,
		})

		ctx := context.Background()

		if err := client.Start(ctx); err != nil {
			t.Logf("Start failed: %v", err)
			return false
		}

		if err := client.Reconnect(ctx); err != nil {
			t.Logf("Reconnect failed: %v", err)
			client.Close()
			return false
		}

		client.Close()

		allPrompts := parsePromptLines(stderrBuf.String())

		var expectedOnce []string
		expectedOnce = append(expectedOnce, systemPrompt)
		expectedOnce = append(expectedOnce, initialContext...)

		expected := append(expectedOnce, expectedOnce...)

		if len(allPrompts) != len(expected) {
			t.Logf("expected %d prompt messages, got %d", len(expected), len(allPrompts))
			t.Logf("expected: %v", expected)
			t.Logf("got:      %v", allPrompts)
			return false
		}

		for i := range expected {
			if allPrompts[i] != expected[i] {
				t.Logf("mismatch at index %d: expected %q, got %q", i, expected[i], allPrompts[i])
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 50}); err != nil {
		t.Errorf("Reconnection replays priming property failed: %v", err)
	}
}

func buildMockServerBinary(t *testing.T) string {
	t.Helper()
	binPath := t.TempDir() + "/mockserver.test"
	cmd := exec.Command("go", "test", "-c", "-o", binPath, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test binary: %v\n%s", err, out)
	}
	return binPath
}

func parsePromptLines(stderr string) []string {
	var prompts []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "PROMPT:") {
			prompts = append(prompts, strings.TrimPrefix(line, "PROMPT:"))
		}
	}
	return prompts
}

func randomAlphanumeric(rnd *rand.Rand, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rnd.Intn(len(chars))]
	}
	return string(b)
}

package agentcore_test

import (
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
	"github.com/nisimpson/bond/runtime/agentcore"
)

func TestNewHTTPHandler(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}
	h := agentcore.NewHTTPHandler(agent, agentcore.Options{})
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.Port() != agentcore.PortHTTP {
		t.Fatalf("expected %q, got %q", agentcore.PortHTTP, h.Port())
	}
}

func TestNewA2AHandler(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}
	h := agentcore.NewA2AHandler(agent, agentcore.Options{})
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.Port() != agentcore.PortA2A {
		t.Fatalf("expected %q, got %q", agentcore.PortA2A, h.Port())
	}
}

func TestNewMCPHandler(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}
	h := agentcore.NewMCPHandler(agent, agentcore.Options{})
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.Port() != agentcore.PortMCP {
		t.Fatalf("expected %q, got %q", agentcore.PortMCP, h.Port())
	}
}

func TestNewHTTPHandler_WithAgentOptions(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}
	opts := agentcore.Options{}
	opts.AgentOptions = bond.AgentOptions{MaxTurns: 5}

	h := agentcore.NewHTTPHandler(agent, opts)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestPortConstants(t *testing.T) {
	if agentcore.PortA2A != ":9000" {
		t.Fatalf("expected ':9000', got %q", agentcore.PortA2A)
	}
	if agentcore.PortHTTP != ":8080" {
		t.Fatalf("expected ':8080', got %q", agentcore.PortHTTP)
	}
	if agentcore.PortMCP != ":8000" {
		t.Fatalf("expected ':8000', got %q", agentcore.PortMCP)
	}
}

func TestSessionFromContext_NoSession(t *testing.T) {
	// When no session is injected, SessionFromContext still returns non-nil.
	session := agentcore.SessionFromContext(t.Context())
	if session.SessionID != "" {
		t.Fatalf("expected empty session ID, got %q", session.SessionID)
	}
}

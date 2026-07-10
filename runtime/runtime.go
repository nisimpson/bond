// Package runtime provides protocol handlers for serving bond agents over
// A2A, HTTP, and MCP. Handlers are generic and configurable — use them
// directly for custom deployments, or use runtime/agentcore for
// AWS Bedrock AgentCore defaults.
package runtime

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/nisimpson/bond"
)

// HealthStatus represents the health state of the agent.
type HealthStatus string

const (
	Healthy     HealthStatus = "Healthy"
	HealthyBusy HealthStatus = "HealthyBusy"
)

// PingHandler returns the current health status.
// Return an error to signal an unhealthy state (non-200 response).
type PingHandler func(ctx context.Context) (HealthStatus, error)

// Options is the shared configuration for all runtime handlers.
type Options struct {
	// Card is the A2A agent card for discovery.
	Card *a2a.AgentCard
	// AgentOptions configures the bond loop (tools, plugins, max turns).
	AgentOptions bond.AgentOptions
	// Ping is an optional health check handler. If nil, returns Healthy.
	Ping PingHandler
	// A2AHandlerOptions are additional options passed to the a2asrv request handler.
	A2AHandlerOptions []a2asrv.RequestHandlerOption
	// Middleware wraps the primary protocol handler (optional).
	Middleware func(http.Handler) http.Handler
}

// HandlePing writes a health check response.
func HandlePing(opts Options, w http.ResponseWriter, r *http.Request) {
	status := Healthy
	if opts.Ping != nil {
		s, err := opts.Ping(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		status = s
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": string(status)})
}

// DefaultAgentCard returns a minimal agent card when none is configured.
func DefaultAgentCard() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "bond-agent",
		Description:        "A bond-powered agent",
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
	}
}

// NewBondExecutor creates the standard a2asrv.AgentExecutor that bridges
// a bond.Agent to the A2A protocol.
func NewBondExecutor(agent bond.Agent, opts bond.AgentOptions) a2asrv.AgentExecutor {
	return &bondExecutor{agent: agent, opts: opts}
}

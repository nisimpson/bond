// Package agentcore provides convenience constructors for running bond agents
// on AWS Bedrock AgentCore Runtime. It wraps the generic runtime package
// with AgentCore-specific defaults (ports, paths, session headers).
//
// For custom deployments, use the runtime package directly.
package agentcore

import (
	"context"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/runtime"
)

// Standard AgentCore ports.
const (
	PortA2A  = ":9000"
	PortHTTP = ":8080"
	PortMCP  = ":8000"
)

// Options configures AgentCore handlers.
type Options struct {
	runtime.Options
	// ShutdownTimeout is how long to wait for in-flight requests during graceful
	// shutdown. Defaults to 30 seconds if zero.
	ShutdownTimeout time.Duration
}

// NewA2AHandler creates an A2A handler with AgentCore defaults (port 9000,
// session middleware).
func NewA2AHandler(agent bond.Agent, opts Options) *runtime.A2AHandler {
	return runtime.NewA2AHandler(agent, runtime.A2AOptions{
		Options:  opts.Options,
		Port:     PortA2A,
		PingPath: "/ping",
	})
}

// NewA2AHandlerFromExecutor creates an A2A handler from a custom executor
// with AgentCore defaults.
func NewA2AHandlerFromExecutor(executor a2asrv.AgentExecutor, opts Options) *runtime.A2AHandler {
	return runtime.NewA2AHandlerFromExecutor(executor, runtime.A2AOptions{
		Options:  opts.Options,
		Port:     PortA2A,
		PingPath: "/ping",
	})
}

// NewHTTPHandler creates an HTTP handler with AgentCore defaults (port 8080,
// /invocations path).
func NewHTTPHandler(agent bond.Agent, opts Options) *runtime.HTTPHandler {
	return runtime.NewHTTPHandler(agent, runtime.HTTPOptions{
		Options:         opts.Options,
		Port:            PortHTTP,
		InvocationsPath: "/invocations",
		PingPath:        "/ping",
	})
}

// NewMCPHandler creates an MCP handler with AgentCore defaults (port 8000,
// /mcp path).
func NewMCPHandler(agent bond.Agent, opts Options) *runtime.MCPHandler {
	return runtime.NewMCPHandler(agent, runtime.MCPOptions{
		Options:  opts.Options,
		Port:     PortMCP,
		MCPPath:  "/mcp",
		PingPath: "/ping",
	})
}

// NewMCPHandlerFromExecutor creates an MCP handler from a custom executor
// with AgentCore defaults.
func NewMCPHandlerFromExecutor(executor a2asrv.AgentExecutor, opts Options) *runtime.MCPHandler {
	return runtime.NewMCPHandlerFromExecutor(executor, runtime.MCPOptions{
		Options:  opts.Options,
		Port:     PortMCP,
		MCPPath:  "/mcp",
		PingPath: "/ping",
	})
}

// Serve creates A2A, HTTP, and MCP handlers and serves them on their
// respective AgentCore ports. Blocks until SIGTERM/SIGINT is received,
// then performs graceful shutdown.
func Serve(agent bond.Agent, opts Options) error {
	a2aHandler := NewA2AHandler(agent, opts)
	httpHandler := NewHTTPHandler(agent, opts)
	mcpHandler := NewMCPHandler(agent, opts)

	timeout := opts.ShutdownTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	reqCtx, cancelReqs := context.WithCancel(context.Background())
	defer cancelReqs()

	baseContext := func(_ net.Listener) context.Context { return reqCtx }

	a2aServer := &http.Server{Addr: a2aHandler.Port(), Handler: a2aHandler, BaseContext: baseContext}
	httpServer := &http.Server{Addr: httpHandler.Port(), Handler: httpHandler, BaseContext: baseContext}
	mcpServer := &http.Server{Addr: mcpHandler.Port(), Handler: mcpHandler, BaseContext: baseContext}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	errs := make(chan error, 3)
	go func() { errs <- a2aServer.ListenAndServe() }()
	go func() { errs <- httpServer.ListenAndServe() }()
	go func() { errs <- mcpServer.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), timeout)
		defer shutdownCancel()

		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); _ = a2aServer.Shutdown(shutdownCtx) }()
		go func() { defer wg.Done(); _ = httpServer.Shutdown(shutdownCtx) }()
		go func() { defer wg.Done(); _ = mcpServer.Shutdown(shutdownCtx) }()
		wg.Wait()

		cancelReqs()
		return nil
	case err := <-errs:
		return err
	}
}

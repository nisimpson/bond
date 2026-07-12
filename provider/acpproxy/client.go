package acpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/nisimpson/bond"
)

// Requirement: 3.1 — THE ACP_Client SHALL provide a constructor that accepts a Transport
// and ClientOptions.
// Requirement: 3.2 — THE ACP_Client SHALL provide a convenience constructor that accepts
// a command string, arguments, ClientOptions, and StdioOptions, creating the StdioProcess internally.
// Requirement: 3.3 — THE ACP_Client SHALL send an "initialize" JSON-RPC request during Start().
// Requirement: 3.4 — THE ACP_Client SHALL parse the initialize response and store the
// external agent's AgentInfo and Capabilities.
// Requirement: 3.5 — THE ACP_Client SHALL send "session/new" with the configured WorkingDir
// after successful initialization.
// Requirement: 3.6 — THE ACP_Client SHALL store the session_id returned by session/new.
// Requirement: 10.1 — constructor accepts Transport and client-specific options.
// Requirement: 10.2 — convenience constructor that creates StdioProcess internally.
// Requirement: 10.3 — accept working directory option.
// Requirement: 10.4 — accept Permission_Tier or custom Permission_Policy.
// Requirement: 10.5 — accept system prompt and initial context.
// Requirement: 10.6 — default CancelTimeout to 5 seconds if unset.
// Requirement: 10.7 — default PermissionTier to TierYOLO if neither tier nor policy configured.
// Requirement: 10.8 — expose Agent(), AgentInfo(), Capabilities() accessors.
// Requirement: 11.5 — Close() is idempotent.
// Requirement: 8.1 — send system prompt as first session/prompt after session creation.
// Requirement: 8.2 — consume the full response (drain notifications) for system prompt.
// Requirement: 8.3 — send InitialContext messages sequentially after system prompt.
// Requirement: 8.4 — consume responses for each InitialContext message in order.

// acpProtocolVersion is the protocol version sent during initialization.
const acpProtocolVersion = 1

// defaultCancelTimeout is used when ClientOptions.CancelTimeout is zero.
const defaultCancelTimeout = 5 * time.Second

// Internal protocol message types for the ACP handshake.

type initializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientInfo         clientInfo         `json:"clientInfo"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type clientCapabilities struct {
	// Empty for now — we don't implement fs or terminal methods.
}

type initializeResponse struct {
	ProtocolVersion   int          `json:"protocolVersion"`
	AgentCapabilities Capabilities `json:"agentCapabilities"`
	AgentInfo         AgentInfo    `json:"agentInfo"`
}

type sessionNewRequest struct {
	CWD        string        `json:"cwd"`
	MCPServers []interface{} `json:"mcpServers"`
}

type sessionNewResponse struct {
	SessionID string `json:"sessionId"`
}

type sessionPromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

// contentBlock is a single content item in a session/prompt request.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type sessionPromptResponse struct {
	StopReason string `json:"stopReason"`
}

// Client manages the ACP protocol lifecycle: initialization, session management,
// prompt submission, permission handling, and reconnection.
type Client struct {
	transport  ReadWriter
	dispatcher *Dispatcher
	opts       ClientOptions

	mu        sync.Mutex
	sessionID string
	agentInfo AgentInfo
	caps      Capabilities
	started   bool
	closed    bool
	proxy     *Agent
	closeOnce sync.Once
}

// NewClient creates a Client using an existing Transport.
// CancelTimeout defaults to 5 seconds if zero.
// PermissionTier defaults to TierYOLO (zero value) when unset.
//
// If the transport supports reconnection (implements Resettable),
// Client.Reconnect() will detect it via interface assertion.
// For StdioProcess-based clients, use NewClientFromCommand instead
// which handles this automatically.
func NewClient(transport ReadWriter, opts ClientOptions) *Client {
	applyDefaults(&opts)
	return &Client{
		transport: transport,
		opts:      opts,
	}
}

// applyDefaults sets default values for unset ClientOptions fields.
func applyDefaults(opts *ClientOptions) {
	if opts.CancelTimeout == 0 {
		opts.CancelTimeout = defaultCancelTimeout
	}
	// PermissionTier defaults to TierYOLO (zero value) when unset,
	// so no special handling is needed.
}

// Start performs the ACP initialization handshake:
//  1. Starts the StdioProcess (if created via NewClientFromCommand)
//  2. Creates and starts the Dispatcher
//  3. Sends "initialize" and parses the response
//  4. Sends "session/new" with WorkingDir
//  5. Runs the priming sequence (system prompt + initial context)
//  6. Creates the ProxyAgent
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return ErrAlreadyStarted
	}
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.mu.Unlock()

	// Step 1: Create and start dispatcher.
	c.dispatcher = NewDispatcher(c.transport)
	c.dispatcher.Start()

	// Step 2: Send initialize request.
	initResp, err := c.dispatcher.Request(ctx, MethodInitialize, &initializeRequest{
		ProtocolVersion:    acpProtocolVersion,
		ClientInfo:         clientInfo{Name: "bond", Version: "0.1.0"},
		ClientCapabilities: clientCapabilities{},
	})
	if err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: initialize: %w", err)
	}

	var initResult initializeResponse
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: parse initialize response: %w", err)
	}

	c.mu.Lock()
	c.agentInfo = initResult.AgentInfo
	c.caps = initResult.AgentCapabilities
	c.mu.Unlock()

	// Step 4: Send session/new request.
	sessionResp, err := c.dispatcher.Request(ctx, MethodSessionNew, &sessionNewRequest{
		CWD:        c.opts.WorkingDir,
		MCPServers: []interface{}{},
	})
	if err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: session/new: %w", err)
	}

	var sessionResult sessionNewResponse
	if err := json.Unmarshal(sessionResp.Result, &sessionResult); err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: parse session/new response: %w", err)
	}

	c.mu.Lock()
	c.sessionID = sessionResult.SessionID
	c.mu.Unlock()

	// Step 5: Run priming sequence.
	if err := c.runPriming(ctx); err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: priming: %w", err)
	}

	// Step 6: Create proxy agent.
	c.mu.Lock()
	c.proxy = &Agent{client: c}
	c.started = true
	c.mu.Unlock()

	return nil
}

// runPriming sends the system prompt and initial context messages, consuming
// their full responses. Notifications that arrive during priming (e.g.,
// agent_message_chunk) are drained from the notification channel after each
// prompt completes.
func (c *Client) runPriming(ctx context.Context) error {
	// Send system prompt if configured.
	if c.opts.SystemPrompt != "" {
		if err := c.sendPrimingPrompt(ctx, c.opts.SystemPrompt); err != nil {
			return fmt.Errorf("system prompt: %w", err)
		}
	}

	// Send initial context messages sequentially.
	for i, msg := range c.opts.InitialContext {
		if err := c.sendPrimingPrompt(ctx, msg); err != nil {
			return fmt.Errorf("initial context [%d]: %w", i, err)
		}
	}

	return nil
}

// sendPrimingPrompt sends a session/prompt request and waits for the response.
// After the response is received, it drains any accumulated notifications from
// the dispatcher's notification channel.
func (c *Client) sendPrimingPrompt(ctx context.Context, text string) error {
	_, err := c.dispatcher.Request(ctx, MethodSessionPrompt, &sessionPromptRequest{
		SessionID: c.sessionID,
		Prompt:    []contentBlock{{Type: "text", Text: text}},
	})
	if err != nil {
		return err
	}

	// Drain accumulated notifications (agent_message_chunk events from the
	// priming response). These are irrelevant to the caller.
	c.drainNotifications()
	return nil
}

// drainNotifications empties the dispatcher's notification channel without
// blocking. This discards notifications that accumulated during priming.
func (c *Client) drainNotifications() {
	for {
		select {
		case <-c.dispatcher.Notifications():
			// discard
		default:
			return
		}
	}
}

// Close performs an idempotent shutdown of the client. It stops the dispatcher
// and closes the transport if it implements io.Closer.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		if c.dispatcher != nil {
			c.dispatcher.Stop()
		}
		if closer, ok := c.transport.(io.Closer); ok {
			err = closer.Close()
		}
	})
	return err
}

// Agent returns the ProxyAgent that implements bond.Agent. Returns nil if
// Start() has not been called successfully.
func (c *Client) Agent() bond.Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.proxy
}

// AgentInfo returns the agent information reported during initialization.
func (c *Client) AgentInfo() AgentInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agentInfo
}

// Capabilities returns the capabilities reported during initialization.
func (c *Client) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Reconnect re-establishes the connection to the external agent. It resets
// the transport, re-performs initialization, creates a new session, and
// replays the priming sequence.
//
// The transport must implement the Resettable interface (e.g., StdioProcess).
// If it does not, Reconnect returns ErrReconnectNotSupported.
//
// Requirement: 9.1 — THE ACP_Client SHALL provide a Reconnect method.
// Requirement: 9.2 — THE ACP_Transport SHALL include a Reset method.
// Requirement: 9.3 — WHEN reconnecting, re-perform initialization (initialize + session/new).
// Requirement: 9.4 — WHEN reconnecting, replay system prompt and initial context.
// Requirement: 9.5 — Connection loss during prompt returns error without auto-reconnect.
// Requirement: 9.6 — If transport doesn't support Reset, return ErrReconnectNotSupported.
func (c *Client) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.mu.Unlock()

	// Determine the Resettable. The transport must implement Resettable
	// (e.g., StdioProcess implements both ReadWriter and Resettable).
	resettable, ok := c.transport.(Resettable)
	if !ok {
		return ErrReconnectNotSupported
	}

	// Stop the current dispatcher to cease reading from the old transport.
	if c.dispatcher != nil {
		c.dispatcher.Stop()
	}

	// Reset the transport (e.g., spawns a new subprocess for StdioProcess,
	// reconnects for a TCP transport, etc.).
	if err := resettable.Reset(); err != nil {
		return fmt.Errorf("acpproxy: reset transport: %w", err)
	}

	// After Reset, the StdioProcess IS the ReadWriter (it internally recreates
	// the Transport). For non-stdio resettables, the existing transport pointer
	// remains valid.
	// No need to reassign c.transport — it already points to the stdio process
	// (or the direct transport that was reset in place).

	// Create and start a new dispatcher on the reset transport.
	c.dispatcher = NewDispatcher(c.transport)
	c.dispatcher.Start()

	// Re-perform initialization: send initialize request.
	initResp, err := c.dispatcher.Request(ctx, MethodInitialize, &initializeRequest{
		ProtocolVersion:    acpProtocolVersion,
		ClientInfo:         clientInfo{Name: "bond", Version: "0.1.0"},
		ClientCapabilities: clientCapabilities{},
	})
	if err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: reconnect initialize: %w", err)
	}

	var initResult initializeResponse
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: reconnect parse initialize response: %w", err)
	}

	c.mu.Lock()
	c.agentInfo = initResult.AgentInfo
	c.caps = initResult.AgentCapabilities
	c.mu.Unlock()

	// Re-perform session creation: send session/new request.
	sessionResp, err := c.dispatcher.Request(ctx, MethodSessionNew, &sessionNewRequest{
		CWD:        c.opts.WorkingDir,
		MCPServers: []interface{}{},
	})
	if err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: reconnect session/new: %w", err)
	}

	var sessionResult sessionNewResponse
	if err := json.Unmarshal(sessionResp.Result, &sessionResult); err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: reconnect parse session/new response: %w", err)
	}

	c.mu.Lock()
	c.sessionID = sessionResult.SessionID
	c.mu.Unlock()

	// Replay the priming sequence (system prompt + initial context).
	if err := c.runPriming(ctx); err != nil {
		c.dispatcher.Stop()
		return fmt.Errorf("acpproxy: reconnect priming: %w", err)
	}

	// Re-create the ProxyAgent with the reconnected client.
	c.mu.Lock()
	c.proxy = &Agent{client: c}
	c.mu.Unlock()

	return nil
}

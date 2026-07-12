package runtime

import (
	"context"
	"fmt"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nisimpson/bond"
)

// MCPOptions configures the MCP handler.
type MCPOptions struct {
	Options
	// Port is the address to serve on (e.g. ":8000").
	Port string
	// MCPPath is the MCP endpoint path. Defaults to "/mcp".
	MCPPath string
	// PingPath is the health check endpoint path. Defaults to "/ping".
	PingPath string
	// SSEMode enables Server-Sent Events transport. When true, the handler
	// uses text/event-stream responses and emits observability log notifications
	// during send_message execution.
	// When false (default), uses application/json responses with no notifications.
	SSEMode bool
}

// MCPHandler serves the MCP protocol (streamable HTTP), exposing A2A
// operations as MCP tools.
type MCPHandler struct {
	mux    *http.ServeMux
	port   string
	bridge *mcpA2ABridge
}

// NewMCPHandler creates an MCP handler wrapping a bond.Agent.
func NewMCPHandler(agent bond.Agent, opts MCPOptions) *MCPHandler {
	executor := &bondExecutor{agent: agent, opts: opts.AgentOptions}
	handler := NewMCPHandlerFromExecutor(executor, opts)
	// Set agent on bridge for SSE mode direct execution.
	handler.bridge.agent = agent
	return handler
}

// NewMCPHandlerFromExecutor creates an MCP handler from a custom executor.
func NewMCPHandlerFromExecutor(executor a2asrv.AgentExecutor, opts MCPOptions) *MCPHandler {
	handlerOpts := append([]a2asrv.RequestHandlerOption{}, opts.A2AHandlerOptions...)
	requestHandler := a2asrv.NewHandler(executor, handlerOpts...)

	// Requirement: 1.4, 3.5, 5.4 — SSE mode fields for direct agent execution
	bridge := &mcpA2ABridge{
		handler:  requestHandler,
		executor: executor,
		agent:    nil, // set by NewMCPHandler when a bond.Agent is available
		opts:     opts.AgentOptions,
		card:     opts.Card,
		sseMode:  opts.SSEMode,
	}

	buildServer := func(_ *http.Request) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{
			Name:    cardName(opts.Options),
			Version: cardVersion(opts.Options),
		}, nil)
		bridge.registerTools(server)
		return server
	}

	// Requirement: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6 — SSEMode maps to JSONResponse
	streamableHandler := mcp.NewStreamableHTTPHandler(buildServer, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: !opts.SSEMode,
	})

	mcpPath := opts.MCPPath
	if mcpPath == "" {
		mcpPath = "/mcp"
	}
	pingPath := opts.PingPath
	if pingPath == "" {
		pingPath = "/ping"
	}
	port := opts.Port
	if port == "" {
		port = ":8000"
	}

	mux := http.NewServeMux()
	mux.Handle(mcpPath, streamableHandler)
	mux.HandleFunc("GET "+pingPath, func(w http.ResponseWriter, r *http.Request) {
		HandlePing(opts.Options, w, r)
	})

	return &MCPHandler{mux: mux, port: port, bridge: bridge}
}

// Port returns the configured port.
func (h *MCPHandler) Port() string { return h.port }

// ServeHTTP implements http.Handler.
func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// ---------------------------------------------------------------------------
// MCP ↔ A2A bridge (same as before, just in runtime package now)
// ---------------------------------------------------------------------------

// Requirement: 1.4, 3.5, 5.4 — SSE mode fields for direct agent execution
type mcpA2ABridge struct {
	handler  a2asrv.RequestHandler
	executor a2asrv.AgentExecutor
	agent    bond.Agent        // direct agent access for SSE mode execution
	opts     bond.AgentOptions // base agent options
	card     *a2a.AgentCard
	sseMode  bool
}

// sendMessageInput is a simplified representation of an A2A message
// that avoids schema generation issues with custom JSON marshaling.
type sendMessageInput struct {
	Role      string            `json:"role,omitempty" jsonschema:"The message role: 'user' or 'agent'. Defaults to 'user'."`
	Message   string            `json:"message,omitempty" jsonschema:"Plain text message. Use for simple text-only messages. Mutually exclusive with 'parts'."`
	Parts     []sendMessagePart `json:"parts,omitempty" jsonschema:"Structured message parts for multi-part or non-text content. Takes precedence over 'message'."`
	ContextID string            `json:"context_id,omitempty" jsonschema:"Optional context ID to continue an existing conversation."`
	TaskID    string            `json:"task_id,omitempty" jsonschema:"Optional task ID to reference an existing task for follow-up messages."`
}

type sendMessagePart struct {
	Text *string `json:"text,omitempty" jsonschema:"Plain text content."`
	Data any     `json:"data,omitempty" jsonschema:"Structured JSON data."`
	URL  *string `json:"url,omitempty" jsonschema:"URL reference."`
}

type taskIDInput struct {
	TaskID string `json:"task_id" jsonschema:"The task ID."`
}

type emptyInput struct{}

func (b *mcpA2ABridge) registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "send_message", Description: "Send a message to the agent."}, b.handleSendMessage)
	mcp.AddTool(server, &mcp.Tool{Name: "get_task", Description: "Get task state by ID."}, b.handleGetTask)
	mcp.AddTool(server, &mcp.Tool{Name: "list_tasks", Description: "List all tasks."}, b.handleListTasks)
	mcp.AddTool(server, &mcp.Tool{Name: "cancel_task", Description: "Cancel a task."}, b.handleCancelTask)
	mcp.AddTool(server, &mcp.Tool{Name: "get_agent_card", Description: "Get the agent card."}, b.handleGetAgentCard)
}

func (b *mcpA2ABridge) handleSendMessage(ctx context.Context, req *mcp.CallToolRequest, in sendMessageInput) (*mcp.CallToolResult, any, error) {
	// Build parts: parts take precedence over message.
	var parts []*a2a.Part
	if len(in.Parts) > 0 {
		for _, p := range in.Parts {
			switch {
			case p.Text != nil:
				parts = append(parts, a2a.NewTextPart(*p.Text))
			case p.Data != nil:
				parts = append(parts, &a2a.Part{Content: a2a.Data{Value: p.Data}})
			case p.URL != nil:
				parts = append(parts, &a2a.Part{Content: a2a.URL(*p.URL)})
			}
		}
	} else if in.Message != "" {
		parts = append(parts, a2a.NewTextPart(in.Message))
	}

	if len(parts) == 0 {
		return mcpError("either 'message' or 'parts' is required"), nil, nil
	}

	role := a2a.MessageRoleUser
	if in.Role == "agent" {
		role = a2a.MessageRoleAgent
	}

	msg := a2a.NewMessage(role, parts...)
	if in.ContextID != "" {
		msg.ContextID = in.ContextID
	}
	if in.TaskID != "" {
		msg.TaskID = a2a.TaskID(in.TaskID)
	}

	// Requirement: 3.1, 3.2, 5.3, 5.5 — SSE mode: invoke agent with observability, accumulate result.
	if b.sseMode && b.agent != nil {
		return b.handleSendMessageSSE(ctx, req, msg)
	}

	// JSON mode: delegate to requestHandler.SendMessage. The handler internally
	// accumulates TaskArtifactUpdateEvent events from the executor and returns
	// only the final result. No incremental events are forwarded over MCP.
	// Requirement: 4.9, 5.1, 5.3
	result, err := b.handler.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return mcpError(err.Error()), nil, nil
	}
	return nil, result, nil
}

// handleSendMessageSSE runs the agent loop directly with the observability plugin
// injected, accumulates the full response, and returns it as a CallToolResult.
// Requirement: 3.1, 3.2, 5.5 — synchronous execution with observability notifications.
func (b *mcpA2ABridge) handleSendMessageSSE(ctx context.Context, req *mcp.CallToolRequest, msg *a2a.Message) (*mcp.CallToolResult, any, error) {
	// Create observability plugin bound to this request's session.
	// Requirement: 2.2 — structured observability notifications via ServerSession.Log.
	obsPlugin := &observabilityPlugin{
		logFn: req.Session.Log,
	}

	// Build agent options with the observability plugin injected.
	opts := b.opts
	opts.Plugins = append(append([]bond.Plugin{}, opts.Plugins...), obsPlugin)

	// Convert A2A message to bond messages.
	messages := a2aMessageToBond(msg)

	// Run the agent synchronously.
	// Requirement: 3.1 — accumulate full response and return as single CallToolResult.
	resp, err := bond.Invoke(ctx, b.agent, messages, opts)
	if err != nil {
		return mcpError(err.Error()), nil, nil
	}

	// Build text content including placeholders for non-text media.
	text := resp.Text
	for _, m := range resp.Media {
		text += fmt.Sprintf("\n[media: type=%s, size=%d bytes]", m.MIMEType, len(m.Data))
	}

	// Return accumulated result as CallToolResult.
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func (b *mcpA2ABridge) handleGetTask(ctx context.Context, req *mcp.CallToolRequest, in taskIDInput) (*mcp.CallToolResult, any, error) {
	task, err := b.handler.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(in.TaskID)})
	if err != nil {
		return mcpError(err.Error()), nil, nil
	}
	return nil, task, nil
}

func (b *mcpA2ABridge) handleListTasks(ctx context.Context, req *mcp.CallToolRequest, in emptyInput) (*mcp.CallToolResult, any, error) {
	result, err := b.handler.ListTasks(ctx, &a2a.ListTasksRequest{})
	if err != nil {
		return mcpError(err.Error()), nil, nil
	}
	return nil, result, nil
}

func (b *mcpA2ABridge) handleCancelTask(ctx context.Context, req *mcp.CallToolRequest, in taskIDInput) (*mcp.CallToolResult, any, error) {
	task, err := b.handler.CancelTask(ctx, &a2a.CancelTaskRequest{ID: a2a.TaskID(in.TaskID)})
	if err != nil {
		return mcpError(err.Error()), nil, nil
	}
	return nil, task, nil
}

func (b *mcpA2ABridge) handleGetAgentCard(ctx context.Context, req *mcp.CallToolRequest, in emptyInput) (*mcp.CallToolResult, any, error) {
	card := b.card
	if card == nil {
		card = DefaultAgentCard()
	}
	return nil, card, nil
}

func mcpError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}, IsError: true}
}

func cardName(opts Options) string {
	if opts.Card != nil && opts.Card.Name != "" {
		return opts.Card.Name
	}
	return "bond-agent"
}

func cardVersion(opts Options) string {
	if opts.Card != nil && opts.Card.Version != "" {
		return opts.Card.Version
	}
	return "1.0.0"
}

// unused import guard
var _ bond.Agent = nil

package acp

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/google/uuid"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent/agentacp"
)

// Options configures the ACP handler.
type Options struct {
	// AgentName is the name reported during initialization.
	AgentName string
	// AgentVersion is the version reported during initialization.
	AgentVersion string
	// AgentOptions configures the bond agent loop (tools, plugins, max turns).
	AgentOptions bond.AgentOptions
	// Transport is the reader/writer pair. Defaults to stdin/stdout.
	Transport ReadWriter
	// Commands is the list of slash commands to advertise to the client.
	Commands []Command
}

// Handler is the ACP protocol handler. It bridges a bond.Agent to the
// ACP JSON-RPC protocol.
type Handler struct {
	agent            bond.Agent
	opts             Options
	transport        ReadWriter
	initialized      bool
	session          *Session
	mu               sync.Mutex
	methods          map[string]methodHandler
	promptDone       chan struct{} // closed when the active prompt goroutine finishes
	toolNotifier     *toolNotifier
	permissionPlugin *permissionPlugin
}

// methodHandler is a function that handles a specific JSON-RPC method.
type methodHandler func(ctx context.Context, msg *Message) error

// NewHandler creates an ACP handler for the given agent.
// Follows the same pattern as NewHTTPHandler, NewA2AHandler, NewMCPHandler.
func NewHandler(agent bond.Agent, opts Options) *Handler {
	transport := opts.Transport
	if transport == nil {
		transport = DefaultTransport()
	}

	h := &Handler{
		agent:     agent,
		opts:      opts,
		transport: transport,
	}

	sessionAccessor := func() *Session {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.session
	}

	// Create the permission plugin for requesting client approval before tool execution.
	h.permissionPlugin = &permissionPlugin{
		transport: transport,
		session:   sessionAccessor,
		pending:   make(map[string]chan permissionResponse),
	}

	// Create the tool notifier plugin for sending tool lifecycle notifications.
	h.toolNotifier = &toolNotifier{
		transport: transport,
		session:   sessionAccessor,
	}

	// Prepend plugins in order: permission (blocks first), then tool notifier (sends "in_progress").
	// This ensures permission is granted before the "in_progress" notification is sent.
	h.opts.AgentOptions.Plugins = append(
		[]bond.Plugin{h.permissionPlugin, h.toolNotifier},
		h.opts.AgentOptions.Plugins...,
	)

	h.methods = map[string]methodHandler{
		agentacp.MethodInitialize:    h.handleInitialize,
		agentacp.MethodSessionNew:    h.handleSessionNew,
		agentacp.MethodSessionPrompt: h.handleSessionPrompt,
		agentacp.MethodSessionCancel: h.handleSessionCancel,
	}

	return h
}

// Serve blocks and processes JSON-RPC messages until the transport reader
// closes or a fatal error occurs. Returns nil on clean EOF shutdown.
func (h *Handler) Serve(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		raw, err := h.transport.ReadMessage()
		if err != nil {
			if err == io.EOF {
				// Wait for any active prompt to finish before returning.
				h.mu.Lock()
				done := h.promptDone
				h.mu.Unlock()
				if done != nil {
					<-done
				}
				return nil
			}
			return err
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			// Malformed JSON — respond with parse error.
			if werr := h.respondParseError(nil); werr != nil {
				return werr
			}
			continue
		}

		// Check if this is a JSON-RPC response (has id and result/error, but no method).
		// These are responses to requests WE sent (e.g., permission requests).
		if msg.JSONRPC == "2.0" && msg.Method == "" && msg.ID != nil && (msg.Result != nil || msg.Error != nil) {
			h.permissionPlugin.handleResponse(&msg)
			continue
		}

		// Validate JSON-RPC envelope: must have jsonrpc == "2.0" and a method field.
		if msg.JSONRPC != "2.0" || msg.Method == "" {
			if werr := h.respondParseError(msg.ID); werr != nil {
				return werr
			}
			continue
		}

		if err := h.dispatch(ctx, &msg); err != nil {
			return err
		}
	}
}

// dispatch routes a parsed message to the appropriate handler method.
func (h *Handler) dispatch(ctx context.Context, msg *Message) error {
	// Pre-initialization guard: reject all methods except agentacp.MethodInitialize
	// if the handler has not been initialized yet.
	if msg.Method != agentacp.MethodInitialize {
		h.mu.Lock()
		initialized := h.initialized
		h.mu.Unlock()

		if !initialized {
			// If it's a request (has an id), respond with error -32002.
			if msg.ID != nil {
				return h.respondError(msg.ID, agentacp.CodeServerNotInit, "server not initialized")
			}
			// If it's a notification, silently ignore it.
			return nil
		}
	}

	handler, exists := h.methods[msg.Method]
	if !exists {
		// Unknown method — only respond if this is a request (has an id).
		if msg.ID != nil {
			return h.respondError(msg.ID, agentacp.CodeMethodNotFound, "method not found: "+msg.Method)
		}
		// Notifications for unknown methods are silently ignored.
		return nil
	}

	return handler(ctx, msg)
}

// respondError sends a JSON-RPC error response.
func (h *Handler) respondError(id *json.RawMessage, code int, message string) error {
	resp := Message{
		JSONRPC: "2.0",
		ID:      id,
		Error: &ErrorObject{
			Code:    code,
			Message: message,
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return h.transport.WriteMessage(data)
}

// respondResult sends a JSON-RPC success response.
func (h *Handler) respondResult(id *json.RawMessage, result any) error {
	resultData, err := json.Marshal(result)
	if err != nil {
		return h.respondError(id, agentacp.CodeInvalidParams, "failed to marshal result")
	}
	resp := Message{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultData,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return h.transport.WriteMessage(data)
}

// respondParseError sends a -32700 parse error response.
func (h *Handler) respondParseError(id *json.RawMessage) error {
	return h.respondError(id, agentacp.CodeParseError, "parse error")
}

// --- Stub method handlers (to be implemented in later tasks) ---

// initializeParams holds the parsed params for the initialize request.
type initializeParams struct {
	ProtocolVersion *int `json:"protocolVersion"`
}

// initializeResult is the success response for the initialize request.
type initializeResult struct {
	ProtocolVersion int                    `json:"protocolVersion"`
	Capabilities    initializeCapabilities `json:"capabilities"`
	AgentInfo       initializeAgentInfo    `json:"agentInfo"`
}

type initializeCapabilities struct {
	PromptCapabilities promptCapabilities `json:"promptCapabilities"`
}

type promptCapabilities struct {
	TextSupported bool `json:"textSupported"`
}

type initializeAgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (h *Handler) handleInitialize(ctx context.Context, msg *Message) error {
	// Notifications don't get responses.
	if msg.ID == nil {
		return nil
	}

	// Parse params.
	var params initializeParams
	if msg.Params != nil {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return h.respondError(msg.ID, agentacp.CodeInvalidParams, "invalid params: "+err.Error())
		}
	}

	// Validate protocolVersion is present.
	if params.ProtocolVersion == nil {
		return h.respondError(msg.ID, agentacp.CodeInvalidParams, "protocolVersion field is required")
	}

	// Validate protocolVersion value.
	if *params.ProtocolVersion != 1 {
		return h.respondError(msg.ID, agentacp.CodeInvalidParams, "unsupported protocol version")
	}

	// Check if already initialized (thread-safe).
	h.mu.Lock()
	if h.initialized {
		h.mu.Unlock()
		return h.respondError(msg.ID, agentacp.CodeInvalidRequest, "already initialized")
	}
	h.initialized = true
	h.mu.Unlock()

	// Respond with success.
	result := initializeResult{
		ProtocolVersion: 1,
		Capabilities: initializeCapabilities{
			PromptCapabilities: promptCapabilities{
				TextSupported: true,
			},
		},
		AgentInfo: initializeAgentInfo{
			Name:    h.opts.AgentName,
			Version: h.opts.AgentVersion,
		},
	}

	return h.respondResult(msg.ID, result)
}

// sessionNewParams holds the parsed params for the session/new request.
type sessionNewParams struct {
	CWD string `json:"cwd"`
}

// sessionNewResult is the success response for the session/new request.
type sessionNewResult struct {
	SessionID string `json:"session_id"`
}

// sessionUpdateNotification is a session/update notification sent to the client.
type sessionUpdateNotification struct {
	SessionID string    `json:"sessionId"`
	Type      string    `json:"type"`
	Commands  []Command `json:"commands"`
}

func (h *Handler) handleSessionNew(ctx context.Context, msg *Message) error {
	// Notifications don't get responses.
	if msg.ID == nil {
		return nil
	}

	// Parse params to extract cwd.
	var params sessionNewParams
	if msg.Params != nil {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return h.respondError(msg.ID, agentacp.CodeInvalidParams, "invalid params: "+err.Error())
		}
	}

	// If a session already exists, close it (cancel any active prompt).
	h.mu.Lock()
	if h.session != nil {
		h.session.close()
	}

	// Generate a new UUID v4 for session ID (crypto/rand-backed via google/uuid).
	sessionID := uuid.New().String()
	h.session = newSession(sessionID, params.CWD)
	h.mu.Unlock()

	// Respond with the session_id.
	if err := h.respondResult(msg.ID, sessionNewResult{SessionID: sessionID}); err != nil {
		return err
	}

	// Send session/update notification with available_commands.
	notification := Message{
		JSONRPC: "2.0",
		Method:  agentacp.MethodSessionUpdate,
	}
	notifParams := sessionUpdateNotification{
		SessionID: sessionID,
		Type:      agentacp.UpdateTypeAvailableCommands,
		Commands:  h.opts.Commands,
	}
	// If commands is nil, use empty slice for JSON serialization.
	if notifParams.Commands == nil {
		notifParams.Commands = []Command{}
	}
	paramsData, err := json.Marshal(notifParams)
	if err != nil {
		return err
	}
	notification.Params = paramsData

	notifData, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	return h.transport.WriteMessage(notifData)
}

// sessionPromptParams holds the parsed params for the session/prompt request.
type sessionPromptParams struct {
	Message string `json:"message"`
}

// sessionPromptResult is the success response for session/prompt.
type sessionPromptResult struct {
	StopReason string `json:"stop_reason"`
}

// agentMessageChunkNotification is the session/update notification for text deltas.
type agentMessageChunkNotification struct {
	SessionID string `json:"sessionId"`
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func (h *Handler) handleSessionPrompt(ctx context.Context, msg *Message) error {
	// Notifications don't get responses.
	if msg.ID == nil {
		return nil
	}

	// Check for active session.
	h.mu.Lock()
	session := h.session
	// Wait for any previous prompt to finish before starting a new one.
	prevDone := h.promptDone
	h.mu.Unlock()

	if prevDone != nil {
		<-prevDone
	}

	if session == nil {
		return h.respondError(msg.ID, agentacp.CodeNoActiveSession, "no active session")
	}

	// Parse params to get user message.
	var params sessionPromptParams
	if msg.Params != nil {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return h.respondError(msg.ID, agentacp.CodeInvalidParams, "invalid params: "+err.Error())
		}
	}

	// Create cancelable context for this prompt turn.
	promptCtx, promptCancel := context.WithCancel(ctx)

	// Mark prompt as active and store cancel function.
	session.mu.Lock()
	session.promptActive = true
	session.promptCancel = promptCancel
	session.mu.Unlock()

	// Create a done channel so the Serve loop can wait on EOF.
	done := make(chan struct{})
	h.mu.Lock()
	h.promptDone = done
	h.mu.Unlock()

	// Run the streaming in a goroutine so the Serve loop can continue
	// reading messages (e.g., session/cancel).
	go func() {
		defer close(done)
		defer func() {
			session.mu.Lock()
			session.promptActive = false
			session.promptCancel = nil
			session.mu.Unlock()
			promptCancel()

			h.mu.Lock()
			h.promptDone = nil
			h.mu.Unlock()
		}()

		// Append user message to history.
		userMsg := bond.Message{
			Role:    bond.RoleUser,
			Content: []bond.Block{&bond.TextBlock{Text: params.Message}},
		}
		session.History = append(session.History, userMsg)

		// Generate a stable messageId for this prompt turn.
		messageID := uuid.New().String()

		// Stream from the agent.
		var textBuf string
		var stopReason string

		for event, err := range bond.Stream(promptCtx, h.agent, session.History, h.opts.AgentOptions) {
			if err != nil {
				// Check if context was cancelled (session/cancel).
				if promptCtx.Err() != nil {
					_ = h.respondResult(msg.ID, sessionPromptResult{StopReason: "cancelled"})
					return
				}
				_ = h.respondError(msg.ID, agentacp.CodeInternalError, "agent stream error: "+err.Error())
				return
			}

			switch event.Type {
			case bond.StreamEventTextDelta:
				textBuf += event.TextDelta

				// Send session/update notification with agent_message_chunk.
				notifParams := agentMessageChunkNotification{
					SessionID: session.ID,
					Type:      "agent_message_chunk",
					MessageID: messageID,
					Delta:     event.TextDelta,
				}
				paramsData, merr := json.Marshal(notifParams)
				if merr != nil {
					return
				}
				notification := Message{
					JSONRPC: "2.0",
					Method:  agentacp.MethodSessionUpdate,
					Params:  paramsData,
				}
				nData, merr2 := json.Marshal(notification)
				if merr2 != nil {
					return
				}
				if werr := h.transport.WriteMessage(nData); werr != nil {
					return
				}

			case bond.StreamEventToolUse:
				// Send tool_call "pending" notification.
				if event.ToolUse != nil {
					if werr := h.toolNotifier.sendToolCallPending(event.ToolUse); werr != nil {
						return
					}
				}

			case bond.StreamEventStop:
				// Map Bond stop reasons to ACP stop reasons.
				switch event.StopReason {
				case bond.StopReasonEnd:
					stopReason = "end_turn"
				case bond.StopReasonLength:
					stopReason = "max_tokens"
				default:
					stopReason = "end_turn"
				}
			}
		}

		// Check if context was cancelled during streaming.
		if promptCtx.Err() != nil {
			_ = h.respondResult(msg.ID, sessionPromptResult{StopReason: "cancelled"})
			return
		}

		// Append assistant response to session history.
		// Always append to maintain alternating user/assistant invariant,
		// even if the agent produced no text (e.g., only tool use events).
		assistantMsg := bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: textBuf}},
		}
		session.History = append(session.History, assistantMsg)

		// Default stop reason if none was received.
		if stopReason == "" {
			stopReason = "end_turn"
		}

		_ = h.respondResult(msg.ID, sessionPromptResult{StopReason: stopReason})
	}()

	return nil
}

func (h *Handler) handleSessionCancel(ctx context.Context, msg *Message) error {
	// session/cancel is always a notification (no id), so no response needed.

	// Check if there's an active session with a prompt in progress.
	h.mu.Lock()
	session := h.session
	h.mu.Unlock()

	if session == nil {
		return nil
	}

	// Cancel the active prompt's context if one is running.
	session.mu.Lock()
	if session.promptActive && session.promptCancel != nil {
		session.promptCancel()
	}
	session.mu.Unlock()

	return nil
}

// Package agentacp provides ACP (Agent Client Protocol) building blocks and a
// proxy client for connecting to external ACP-compatible agents.
//
// This package serves two purposes:
//
//  1. Shared protocol types: Message, ErrorObject, error codes, Command, and
//     Transport are the canonical definitions used by both this proxy client and
//     the runtime/acp handler package.
//
//  2. Proxy client: Client, ProxyAgent, and acpio.StdioProcess enable bond agents to
//     delegate work to external ACP agents (such as Kiro, Claude Code, or any
//     ACP-speaking process). The Client manages protocol lifecycle, and
//     ProxyAgent implements bond.Agent so the external agent can be used anywhere
//     a local agent would be — in bond.Stream, bond.Invoke, or multi-agent graphs.
//
// # Quick Start
//
//	client := agentacp.NewClientFromCommand("kiro", nil,
//	    agentacp.ClientOptions{WorkingDir: "/my/project"},
//	    acpio.StdioOptions{},
//	)
//	if err := client.Start(ctx); err != nil { ... }
//	defer client.Close()
//
//	agent := client.Agent()
//	// Use agent with bond.Stream, bond.Invoke, etc.
package agentacp

import "encoding/json"

// Requirement: 12.1 — THE ACP_Client SHALL write JSON-RPC 2.0 requests and notifications
// to the Transport as single lines of compact JSON terminated by a newline character.

// Requirement: 12.2 — THE ACP_Client SHALL read JSON-RPC 2.0 responses and notifications
// from the Transport as single lines of compact JSON terminated by a newline character.

// Message is the base envelope for all JSON-RPC 2.0 messages.
type Message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`     // absent for notifications
	Method  string           `json:"method,omitempty"` // present for requests/notifications
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"` // present for success responses
	Error   *ErrorObject     `json:"error,omitempty"`  // present for error responses
}

// ErrorObject represents a JSON-RPC 2.0 error.
type ErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Standard JSON-RPC error codes.
const (
	CodeParseError      = -32700
	CodeInvalidRequest  = -32600
	CodeMethodNotFound  = -32601
	CodeInvalidParams   = -32602
	CodeInternalError   = -32603
	CodeServerNotInit   = -32002
	CodeNoActiveSession = -32001
)

// Command represents a slash command advertised to the editor.
type Command struct {
	// Name is the command trigger (e.g. "/fix", "/explain").
	Name string `json:"name"`
	// Description is a human-readable explanation shown in the command palette.
	Description string `json:"description"`
}

// ACP JSON-RPC method names.
const (
	MethodInitialize        = "initialize"
	MethodSessionNew        = "session/new"
	MethodSessionPrompt     = "session/prompt"
	MethodSessionCancel     = "session/cancel"
	MethodSessionUpdate     = "session/update"
	MethodRequestPermission = "session/request_permission"
)

// ACP notification update types (used in session/update params).
const (
	UpdateTypeAgentMessageChunk = "agent_message_chunk"
	UpdateTypeToolCall          = "tool_call"
	UpdateTypeToolCallUpdate    = "tool_call_update"
	UpdateTypeAvailableCommands = "available_commands"
)

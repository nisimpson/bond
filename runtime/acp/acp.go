// Package acp provides an ACP (Agent Client Protocol) handler that serves
// bond agents over JSON-RPC 2.0 via stdio or other transports. ACP enables
// code editors (Zed, JetBrains, VS Code) to connect to bond agents as
// coding assistants.
package acp

import "encoding/json"

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

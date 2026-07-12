// Package acp provides an ACP (Agent Client Protocol) handler that serves
// bond agents over JSON-RPC 2.0 via stdio or other transports. ACP enables
// code editors (Zed, JetBrains, VS Code) to connect to bond agents as
// coding assistants.
//
// This package uses shared protocol types from agent/agentacp (Message,
// ErrorObject, error codes, method constants) and transport implementations
// from agent/agentacp/acpio (Transport, StdioProcess).
package acp

import (
	"github.com/nisimpson/bond/agent/agentacp"
	"github.com/nisimpson/bond/agent/agentacp/acpio"
)

// Protocol types used throughout this package.
type (
	Message     = agentacp.Message
	ErrorObject = agentacp.ErrorObject
	Command     = agentacp.Command
	ReadWriter  = agentacp.ReadWriter
)

// NewTransport creates a ndjson Transport from an io.Reader and io.Writer.
var NewTransport = acpio.NewTransport

// DefaultTransport returns a Transport using os.Stdin and os.Stdout.
var DefaultTransport = acpio.DefaultTransport

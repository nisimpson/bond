// Package acp provides an ACP (Agent Client Protocol) handler that serves
// bond agents over JSON-RPC 2.0 via stdio or other transports. ACP enables
// code editors (Zed, JetBrains, VS Code) to connect to bond agents as
// coding assistants.
//
// This package uses shared protocol types from agent/acpproxy (Message,
// ErrorObject, error codes, method constants) and transport implementations
// from agent/acpproxy/acpio (Transport, StdioProcess).
package acp

import (
	"github.com/nisimpson/bond/provider/acpproxy"
	"github.com/nisimpson/bond/provider/acpproxy/acpio"
)

// Protocol types used throughout this package.
type (
	Message     = acpproxy.Message
	ErrorObject = acpproxy.ErrorObject
	Command     = acpproxy.Command
	ReadWriter  = acpproxy.ReadWriter
)

// NewTransport creates a ndjson Transport from an io.Reader and io.Writer.
var NewTransport = acpio.NewTransport

// DefaultTransport returns a Transport using os.Stdin and os.Stdout.
var DefaultTransport = acpio.DefaultTransport

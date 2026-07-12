package acpproxy

import "encoding/json"

// Requirement 1.1 — The proxy SHALL communicate with the agent child process over
// newline-delimited JSON (ndjson) on stdin/stdout.
// Requirement 1.2 — Each JSON line SHALL be a complete JSON-RPC 2.0 message.
// Requirement 1.6 — The Transport SHALL support a configurable maximum message size
// (default 1 MiB).

// maxScanTokenSize is the maximum size of a single JSON-RPC message line.
// Set to 1 MiB to accommodate large tool results and code content.
const maxScanTokenSize = 1024 * 1024

// Reader reads pre-serialized JSON-RPC messages from a transport.
type Reader interface {
	ReadMessage() (json.RawMessage, error)
}

// Writer writes pre-serialized JSON-RPC messages to a transport.
type Writer interface {
	WriteMessage(json.RawMessage) error
}

// ReadWriter combines Reader and Writer for bidirectional message transport.
// Implementations handle framing (e.g., newline-delimited JSON over stdio,
// HTTP SSE for streaming, WebSocket frames, etc.) while the caller handles
// serialization.
type ReadWriter interface {
	Reader
	Writer
}

// Requirement: 9.2 — transport Reset method for reconnection

// Resettable is an optional interface that transports may implement to support
// reconnection. When Reset is called, the transport re-establishes its
// underlying connection (e.g., StdioProcess spawns a new subprocess, a TCP
// transport reconnects to the server).
type Resettable interface {
	Reset() error
}

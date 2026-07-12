package agentacp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Requirement: 11.1 — JSON-RPC error wrapping
// Requirement: 11.2 — malformed message error
// Requirement: 11.3 — transport error indication

// Sentinel errors for client lifecycle states.
var (
	ErrNotStarted            = errors.New("agentacp: client not started")
	ErrAlreadyStarted        = errors.New("agentacp: client already started")
	ErrClosed                = errors.New("agentacp: client closed")
	ErrConnectionLost        = errors.New("agentacp: connection lost")
	ErrReconnectNotSupported = errors.New("agentacp: transport does not support reconnection")
	ErrCancelTimeout         = errors.New("agentacp: cancel response timeout")
)

// ProtocolError wraps a JSON-RPC error response from the external agent.
type ProtocolError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("agentacp: protocol error %d: %s", e.Code, e.Message)
}

// ParseError indicates a malformed message was received from the external agent.
type ParseError struct {
	Raw []byte
	Err error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("agentacp: parse error: %v", e.Err)
}

func (e *ParseError) Unwrap() error {
	return e.Err
}

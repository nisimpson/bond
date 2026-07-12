package acpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

// Requirement: 12.3 — THE ACP_Client SHALL correctly demultiplex interleaved
// responses and notifications received on the same Transport stream.
// Requirement: 12.4 — THE ACP_Client SHALL match incoming JSON-RPC responses
// to their pending requests using the request id.
// Requirement: 12.6 — THE ACP_Client SHALL generate monotonically increasing
// integer request IDs for outgoing requests.

// Dispatcher multiplexes outgoing JSON-RPC requests on a ReadWriter and
// demultiplexes incoming responses and notifications back to the appropriate
// callers.
type Dispatcher struct {
	transport ReadWriter
	nextID    atomic.Int64
	pending   sync.Map      // map[int64]chan *Message
	notifCh   chan *Message // buffered channel for notifications
	done      chan struct{}
	closeOnce sync.Once
	readErr   atomic.Pointer[error]
}

// notifBufferSize is the capacity of the notification channel.
// If the consumer cannot keep up, notifications are dropped to prevent
// blocking the read loop. In practice, this is unlikely since the ProxyAgent
// consumes notifications synchronously during a prompt.
const notifBufferSize = 256

// NewDispatcher creates a Dispatcher bound to the given transport.
// The notification channel is buffered with capacity notifBufferSize.
func NewDispatcher(transport ReadWriter) *Dispatcher {
	return &Dispatcher{
		transport: transport,
		notifCh:   make(chan *Message, notifBufferSize),
		done:      make(chan struct{}),
	}
}

// Start launches the background read loop that demultiplexes incoming messages.
func (d *Dispatcher) Start() {
	go d.readLoop()
}

// Stop gracefully shuts down the dispatcher. It closes the done channel and
// drains all pending request channels. Safe to call multiple times.
func (d *Dispatcher) Stop() {
	d.closeOnce.Do(func() {
		close(d.done)
		d.pending.Range(func(key, value any) bool {
			ch := value.(chan *Message)
			select {
			case ch <- nil:
			default:
			}
			d.pending.Delete(key)
			return true
		})
	})
}

// Request sends a JSON-RPC request and blocks until a response is received,
// the context is cancelled, or the dispatcher is stopped.
func (d *Dispatcher) Request(ctx context.Context, method string, params any) (*Message, error) {
	// Check if already stopped.
	select {
	case <-d.done:
		return nil, d.connectionError()
	default:
	}

	// Generate monotonically increasing ID.
	id := d.nextID.Add(1)

	// Marshal params.
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("acpproxy: marshal params: %w", err)
		}
		paramsData = data
	}

	// Build the request message.
	idRaw := json.RawMessage(strconv.FormatInt(id, 10))
	msg := &Message{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  method,
		Params:  paramsData,
	}

	// Register pending response channel.
	respCh := make(chan *Message, 1)
	d.pending.Store(id, respCh)

	// Serialize and write the message to transport.
	data, err := json.Marshal(msg)
	if err != nil {
		d.pending.Delete(id)
		return nil, fmt.Errorf("acpproxy: marshal request: %w", err)
	}
	if err := d.transport.WriteMessage(data); err != nil {
		d.pending.Delete(id)
		return nil, fmt.Errorf("acpproxy: write request: %w", err)
	}

	// Wait for response, cancellation, or shutdown.
	select {
	case resp := <-respCh:
		if resp == nil {
			return nil, d.connectionError()
		}
		if resp.Error != nil {
			return nil, &ProtocolError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		return resp, nil
	case <-ctx.Done():
		d.pending.Delete(id)
		return nil, ctx.Err()
	case <-d.done:
		d.pending.Delete(id)
		return nil, d.connectionError()
	}
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (d *Dispatcher) Notify(method string, params any) error {
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("acpproxy: marshal params: %w", err)
		}
		paramsData = data
	}

	msg := &Message{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsData,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("acpproxy: marshal notification: %w", err)
	}
	if err := d.transport.WriteMessage(data); err != nil {
		return fmt.Errorf("acpproxy: write notification: %w", err)
	}
	return nil
}

// Notifications returns the channel on which incoming notifications and
// server-initiated requests are delivered.
func (d *Dispatcher) Notifications() <-chan *Message {
	return d.notifCh
}

// readLoop continuously reads from the transport and routes messages.
func (d *Dispatcher) readLoop() {
	for {
		raw, err := d.transport.ReadMessage()
		if err != nil {
			d.storeReadErr(err)
			d.Stop()
			return
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			// Skip malformed messages — could log in the future.
			continue
		}

		switch {
		case msg.ID != nil && msg.Method == "":
			// Response: has ID, no method — route to pending request.
			id, err := parseID(*msg.ID)
			if err != nil {
				continue
			}
			if val, ok := d.pending.LoadAndDelete(id); ok {
				ch := val.(chan *Message)
				ch <- &msg
			}

		case msg.Method != "" && msg.ID == nil:
			// Notification: has method, no ID.
			select {
			case d.notifCh <- &msg:
			default:
				// Drop if notification channel is full — prevents blocking the read loop.
			}

		case msg.ID != nil && msg.Method != "":
			// Server-initiated request (e.g., permission requests): has both ID and method.
			select {
			case d.notifCh <- &msg:
			default:
			}
		}
	}
}

// storeReadErr atomically stores the read error.
func (d *Dispatcher) storeReadErr(err error) {
	d.readErr.Store(&err)
}

// connectionError returns the stored read error or ErrConnectionLost.
func (d *Dispatcher) connectionError() error {
	if p := d.readErr.Load(); p != nil {
		return fmt.Errorf("%w: %v", ErrConnectionLost, *p)
	}
	return ErrConnectionLost
}

// parseID extracts an int64 from a json.RawMessage ID field.
func parseID(raw json.RawMessage) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	return n.Int64()
}

package acpio

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
)

// maxScanTokenSize is the maximum size of a single JSON-RPC message line.
// Set to 1 MiB to accommodate large tool results and code content.
const maxScanTokenSize = 1024 * 1024

// Transport is the default ReadWriter implementation using newline-delimited
// JSON over an io.Reader/io.Writer pair (e.g., stdio pipes, TCP connections).
type Transport struct {
	reader  *bufio.Scanner
	writer  io.Writer
	writeMu sync.Mutex // serializes writes
}

// NewTransport creates a Transport from an io.Reader and io.Writer.
func NewTransport(r io.Reader, w io.Writer) *Transport {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, maxScanTokenSize), maxScanTokenSize)
	return &Transport{
		reader: scanner,
		writer: w,
	}
}

// ReadMessage reads the next JSON-RPC message from the reader.
// Returns io.EOF when the reader is exhausted.
func (t *Transport) ReadMessage() (json.RawMessage, error) {
	if !t.reader.Scan() {
		if err := t.reader.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	line := t.reader.Bytes()
	// Return a copy so the caller isn't affected by scanner buffer reuse.
	msg := make(json.RawMessage, len(line))
	copy(msg, line)
	return msg, nil
}

// WriteMessage writes pre-serialized JSON data as a single line terminated by
// a newline character. Concurrent calls are serialized via a mutex.
func (t *Transport) WriteMessage(msg json.RawMessage) error {
	data := append(msg, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	_, err := t.writer.Write(data)
	return err
}

// DefaultTransport returns a Transport using os.Stdin and os.Stdout.
func DefaultTransport() *Transport {
	return NewTransport(os.Stdin, os.Stdout)
}

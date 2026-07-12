package acp

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

// Transport handles reading and writing newline-delimited JSON-RPC messages.
type Transport struct {
	reader  *bufio.Scanner
	writer  io.Writer
	writeMu sync.Mutex // serializes writes
}

// NewTransport creates a transport from an io.Reader and io.Writer.
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

// WriteMessage serializes msg to compact JSON, appends a newline, and writes
// it atomically to the underlying writer. Concurrent calls are serialized via
// a mutex.
func (t *Transport) WriteMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	_, err = t.writer.Write(data)
	return err
}

// DefaultTransport returns a Transport using os.Stdin and os.Stdout.
func DefaultTransport() *Transport {
	return NewTransport(os.Stdin, os.Stdout)
}

package agentacp

import (
	"bufio"
	"encoding/json"
	"io"
	"math/rand"
	"sync"
)

// pipeReadWriter is a test-only ReadWriter built on top of io.Pipe pairs.
// It uses newline-delimited JSON framing, identical to acpio.Transport.
type pipeReadWriter struct {
	reader  *bufio.Scanner
	writer  io.Writer
	writeMu sync.Mutex
}

// newPipeReadWriter creates a ReadWriter from an io.Reader and io.Writer,
// using newline-delimited JSON framing. Used in tests where we can't import acpio.
func newPipeReadWriter(r io.Reader, w io.Writer) *pipeReadWriter {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, maxScanTokenSize), maxScanTokenSize)
	return &pipeReadWriter{
		reader: scanner,
		writer: w,
	}
}

func (t *pipeReadWriter) ReadMessage() (json.RawMessage, error) {
	if !t.reader.Scan() {
		if err := t.reader.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	line := t.reader.Bytes()
	msg := make(json.RawMessage, len(line))
	copy(msg, line)
	return msg, nil
}

func (t *pipeReadWriter) WriteMessage(msg json.RawMessage) error {
	data := append(msg, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err := t.writer.Write(data)
	return err
}

// generateAlphanumeric produces a random alphanumeric string of the given length.
func generateAlphanumeric(rnd *rand.Rand, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rnd.Intn(len(chars))]
	}
	return string(b)
}

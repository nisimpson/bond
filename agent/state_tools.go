package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nisimpson/helix"
)

// readStateTool is a tool that reads a key from graph state.
type readStateTool struct {
	state State
}

func (t *readStateTool) Name() string { return "read_state" }
func (t *readStateTool) Description() string {
	return "Read a value from the shared graph state by key."
}
func (t *readStateTool) InputSchema() json.Marshaler {
	return json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"The state key to read"}},"required":["key"]}`)
}

func (t *readStateTool) Run(ctx context.Context, input json.RawMessage) ([]helix.Block, error) {
	var params struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("read_state: unmarshal input: %w", err)
	}

	value, ok := t.state.Get(params.Key)
	if !ok {
		return []helix.Block{&helix.TextBlock{Text: fmt.Sprintf("key %q not found in state", params.Key)}}, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("read_state: marshal value: %w", err)
	}

	return []helix.Block{&helix.TextBlock{Text: string(data)}}, nil
}

// writeStateTool is a tool that writes a key/value to graph state.
type writeStateTool struct {
	state State
}

func (t *writeStateTool) Name() string        { return "write_state" }
func (t *writeStateTool) Description() string { return "Write a value to the shared graph state." }
func (t *writeStateTool) InputSchema() json.Marshaler {
	return json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"The state key to write"},"value":{"description":"The value to store"}},"required":["key","value"]}`)
}

func (t *writeStateTool) Run(ctx context.Context, input json.RawMessage) ([]helix.Block, error) {
	var params struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("write_state: unmarshal input: %w", err)
	}

	t.state.Set(params.Key, params.Value)

	return []helix.Block{&helix.TextBlock{Text: fmt.Sprintf("state[%q] updated", params.Key)}}, nil
}

// stateTools returns the read/write tools for a given State.
func stateTools(state State) []helix.Tool {
	return []helix.Tool{
		&readStateTool{state: state},
		&writeStateTool{state: state},
	}
}

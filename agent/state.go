package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nisimpson/bond"
)

// State is the interface for shared graph state. Implementations can be
// backed by a map, filesystem, database, or any other storage mechanism.
// Implementations must be safe for concurrent use.
type State interface {
	// Get retrieves a value by key. Returns false if the key doesn't exist.
	Get(key string) (any, bool)
	// Set stores a value by key.
	Set(key string, value any)
	// Keys returns all keys currently in state.
	Keys() []string
}

// MapState is a concurrency-safe in-memory [State] implementation.
type MapState struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewMapState creates an empty MapState.
func NewMapState() *MapState {
	return &MapState{data: make(map[string]any)}
}

// NewMapStateFrom creates a MapState pre-populated with the given data.
func NewMapStateFrom(initial map[string]any) *MapState {
	data := make(map[string]any, len(initial))
	for k, v := range initial {
		data[k] = v
	}
	return &MapState{data: data}
}

func (m *MapState) Get(key string) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok
}

func (m *MapState) Set(key string, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
}

func (m *MapState) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys
}

// stateContextKey is the context key for accessing graph state.
type stateContextKey struct{}

// ContextWithState attaches a [State] to the context.
func ContextWithState(ctx context.Context, state State) context.Context {
	return context.WithValue(ctx, stateContextKey{}, state)
}

// StateFromContext retrieves the graph [State] from a context. Returns nil
// if no state is attached (e.g., not running inside a graph).
func StateFromContext(ctx context.Context) State {
	s, _ := ctx.Value(stateContextKey{}).(State)
	return s
}

// Verify interface compliance.
var _ State = (*MapState)(nil)

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

func (t *readStateTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	var params struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("read_state: unmarshal input: %w", err)
	}

	value, ok := t.state.Get(params.Key)
	if !ok {
		return []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("key %q not found in state", params.Key)}}, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("read_state: marshal value: %w", err)
	}

	return []bond.Block{&bond.TextBlock{Text: string(data)}}, nil
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

func (t *writeStateTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	var params struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("write_state: unmarshal input: %w", err)
	}

	t.state.Set(params.Key, params.Value)

	return []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("state[%q] updated", params.Key)}}, nil
}

// stateTools returns the read/write tools for a given State.
func stateTools(state State) []bond.Tool {
	return []bond.Tool{
		&readStateTool{state: state},
		&writeStateTool{state: state},
	}
}

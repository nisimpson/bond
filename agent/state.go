package agent

import "context"

// State is the interface for shared graph state. Implementations can be
// backed by a map, filesystem, database, or any other storage mechanism.
type State interface {
	// Get retrieves a value by key. Returns false if the key doesn't exist.
	Get(key string) (any, bool)
	// Set stores a value by key.
	Set(key string, value any)
	// Keys returns all keys currently in state.
	Keys() []string
}

// MapState is a simple in-memory State implementation backed by a map.
type MapState map[string]any

func (m MapState) Get(key string) (any, bool) {
	v, ok := m[key]
	return v, ok
}

func (m MapState) Set(key string, value any) {
	m[key] = value
}

func (m MapState) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// stateContextKey is the context key for accessing graph state.
type stateContextKey struct{}

// withState attaches a State to the context.
func withState(ctx context.Context, state State) context.Context {
	return context.WithValue(ctx, stateContextKey{}, state)
}

// StateFromContext retrieves the graph State from a context. Returns nil
// if no state is attached (e.g., not running inside a graph).
func StateFromContext(ctx context.Context) State {
	s, _ := ctx.Value(stateContextKey{}).(State)
	return s
}

// Verify interface compliance.
var _ State = (MapState)(nil)

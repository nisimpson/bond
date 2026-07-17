package delegation_test

import (
	"context"
	"encoding/json"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/delegation"
)

type MockRequester struct {
	lastToolName string
	lastInput    json.RawMessage
	response     []bond.Block
	err          error
}

func (r *MockRequester) RequestInput(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error) {
	r.lastToolName = toolName
	r.lastInput = input
	return r.response, r.err
}

// InProcessRequester simulates A2A communication in-process by calling the
// fulfiller directly. This is what the delegation round-trip looks like
// without network transport.
type InProcessRequester struct {
	Fulfiller *delegation.Fulfiller
}

func (r *InProcessRequester) RequestInput(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error) {
	return r.Fulfiller.Execute(ctx, toolName, input)
}

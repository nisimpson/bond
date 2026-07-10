// Package agent provides utilities for using bond agents as tools,
// enabling sub-agent orchestration patterns.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nisimpson/bond"
)

// ToolOptions configures an agent-backed tool.
type ToolOptions struct {
	// Name is the tool identifier the model will reference.
	Name string
	// Description helps the model decide when to delegate to this agent.
	Description string
	// InputSchema describes the expected input. Defaults to {"prompt": string}.
	InputSchema json.Marshaler
	// StreamOptions configures the sub-agent's loop (tools, hooks, plugins, max turns).
	StreamOptions bond.AgentOptions
}

// agentToolInput is the default input contract.
type agentToolInput struct {
	Prompt string `json:"prompt"`
}

// agentToolOutput is the default output contract.
type agentToolOutput struct {
	Response string `json:"response"`
}

// agentTool wraps a bond.Agent as a bond.Tool.
type agentTool struct {
	agent bond.Agent
	opts  ToolOptions
}

func (t *agentTool) Name() string                { return t.opts.Name }
func (t *agentTool) Description() string         { return t.opts.Description }
func (t *agentTool) InputSchema() json.Marshaler { return t.opts.InputSchema }

func (t *agentTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	var in agentToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("agent tool %q: unmarshal input: %w", t.opts.Name, err)
	}

	messages := bond.TextPrompt(in.Prompt)

	var buf strings.Builder
	for event, err := range bond.Stream(ctx, t.agent, messages, t.opts.StreamOptions) {
		if err != nil {
			return nil, fmt.Errorf("agent tool %q: %w", t.opts.Name, err)
		}
		if event.Type == bond.StreamEventTextDelta {
			buf.WriteString(event.TextDelta)
		}
	}

	out := agentToolOutput{Response: buf.String()}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("agent tool %q: marshal output: %w", t.opts.Name, err)
	}

	return []bond.Block{&bond.TextBlock{Text: string(data)}}, nil
}

// AsTool wraps any [bond.Agent] as a [bond.Tool]. When invoked, it runs
// the full agent loop using the input prompt and returns the collected text
// response as the tool result.
//
// The default input schema expects {"prompt": "..."} and the output
// returns {"response": "..."}.
func AsTool(a bond.Agent, opts ToolOptions) bond.Tool {
	if opts.InputSchema == nil {
		opts.InputSchema = defaultInputSchema()
	}
	return &agentTool{agent: a, opts: opts}
}

// defaultInputSchema returns a JSON schema for {"prompt": string}.
func defaultInputSchema() json.Marshaler {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The prompt or task to send to the agent"}},"required":["prompt"]}`)
}

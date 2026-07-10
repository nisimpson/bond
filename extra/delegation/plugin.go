package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nisimpson/bond"
)

// ---------------------------------------------------------------------------
// Shared types
// ---------------------------------------------------------------------------

// Skill describes a tool capability that can be advertised and delegated.
type Skill struct {
	// Name is the tool identifier.
	Name string
	// Description helps the server model decide when to use this tool.
	Description string
	// InputSchema is the JSON schema for the tool's expected input.
	InputSchema json.Marshaler
}

// ---------------------------------------------------------------------------
// Server side (receives skills, creates proxy tools)
// ---------------------------------------------------------------------------

// Requester defines the transport for sending "input required" requests to
// a client agent. Implementations handle the A2A protocol details.
type Requester interface {
	// RequestInput sends an "input required" task to the client for the given
	// tool and blocks until the client responds or the context is cancelled.
	RequestInput(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error)
}

// Options configures the delegation plugin (server side).
type Options struct {
	// Requester handles communication with the client agent.
	Requester Requester
	// Skills are the capabilities advertised by the client that this agent
	// claims as proxy tools.
	Skills []Skill
}

// Plugin implements bond.Plugin for the server side of tool delegation.
// It exposes client skills as local tools that delegate back to the client
// when invoked.
type Plugin struct {
	opts  Options
	tools []bond.Tool
}

// New creates a delegation plugin (server side). Each skill becomes a proxy
// tool that sends "input required" to the client when invoked.
func NewPlugin(opts Options) *Plugin {
	tools := make([]bond.Tool, len(opts.Skills))
	for i, skill := range opts.Skills {
		tools[i] = &proxyTool{
			name:        skill.Name,
			description: skill.Description,
			inputSchema: skill.InputSchema,
			requester:   opts.Requester,
		}
	}
	return &Plugin{opts: opts, tools: tools}
}

func (p *Plugin) Name() string                      { return "delegation" }
func (p *Plugin) Tools() []bond.Tool               { return p.tools }
func (p *Plugin) Init(registry *bond.HookRegistry) {}

// proxyTool delegates execution to the client agent via Requester.
type proxyTool struct {
	name        string
	description string
	inputSchema json.Marshaler
	requester   Requester
}

func (t *proxyTool) Name() string                { return t.name }
func (t *proxyTool) Description() string         { return t.description }
func (t *proxyTool) InputSchema() json.Marshaler { return t.inputSchema }

func (t *proxyTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	blocks, err := t.requester.RequestInput(ctx, t.name, input)
	if err != nil {
		return nil, fmt.Errorf("delegation %q: %w", t.name, err)
	}
	return blocks, nil
}

// ---------------------------------------------------------------------------
// Client side (advertises skills, fulfills delegation requests)
// ---------------------------------------------------------------------------

// SkillsFromTools extracts Skills from bond tools for advertisement to
// server agents (via agent card, message metadata, etc.).
func skillsFromTools(tools []bond.Tool) []Skill {
	skills := make([]Skill, len(tools))
	for i, t := range tools {
		skills[i] = Skill{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		}
	}
	return skills
}

// fulfiller handles incoming "input required" delegation requests by
// executing the corresponding local tool.
type fulfiller struct {
	mu    sync.RWMutex
	tools map[string]bond.Tool
}

// newFulfiller creates a fulfiller with the given tools available for
// delegation fulfillment.
func newFulfiller(tools ...bond.Tool) *fulfiller {
	m := make(map[string]bond.Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &fulfiller{tools: m}
}

// Execute handles an incoming delegation request by running the named tool
// with the provided input. Returns the tool's result blocks.
func (f *fulfiller) Execute(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error) {
	f.mu.RLock()
	tool, exists := f.tools[toolName]
	f.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("delegation: unknown tool %q", toolName)
	}

	return tool.Run(ctx, input)
}

// Verify interface compliance.
var _ bond.Plugin = (*Plugin)(nil)

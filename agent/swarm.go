package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/nisimpson/bond"
)

// SwarmAgent represents a named agent in the swarm.
type SwarmAgent struct {
	Agent   bond.Agent
	Options bond.AgentOptions
	// Description tells other agents when and why to transfer to this one.
	// This becomes the transfer tool's description that the active agent sees.
	Description string
}

// SwarmOptions configures a Swarm.
type SwarmOptions struct {
	// State is the shared state for the swarm. If nil, a MapState is created.
	State State
	// MaxHandoffs limits the number of agent transfers. 0 means unlimited.
	MaxHandoffs int
}

// Swarm is an agent that enables dynamic handoffs between multiple agents.
// Each agent can transfer control to another by calling a `transfer_to_<name>`
// tool. The receiving agent picks up with the full conversation history.
//
// Swarm implements [bond.Agent].
//
// Example:
//
//	s := agent.NewSwarm("triage", agent.SwarmOptions{
//	    State: agent.MapState{"user_tier": "premium"},
//	})
//
//	s.AddAgent("triage", &agent.SwarmAgent{Agent: triageAgent, Options: triageOpts})
//	s.AddAgent("billing", &agent.SwarmAgent{Agent: billingAgent, Options: billingOpts})
//	s.AddAgent("tech", &agent.SwarmAgent{Agent: techAgent, Options: techOpts})
//
//	// Use like any other agent. Agents transfer between each other via tool calls.
//	for event, err := range bond.Stream(ctx, s, bond.TextPrompt("my bill is wrong"), bond.StreamOptions{}) {
//	    fmt.Print(event.TextDelta)
//	}
type Swarm struct {
	agents map[string]*SwarmAgent
	entry  string
	state  State
	opts   SwarmOptions
}

// NewSwarm creates a swarm with the given initial active agent name.
func NewSwarm(entry string, opts SwarmOptions) *Swarm {
	state := opts.State
	if state == nil {
		state = NewMapState()
	}
	return &Swarm{
		agents: make(map[string]*SwarmAgent),
		entry:  entry,
		state:  state,
		opts:   opts,
	}
}

// AddAgent registers a named agent in the swarm.
func (s *Swarm) AddAgent(name string, agent *SwarmAgent) {
	s.agents[name] = agent
}

// Stream implements [bond.Agent]. It runs the active agent and handles
// transfer_to_<name> tool calls to switch between agents.
func (s *Swarm) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		ctx = ContextWithState(ctx, s.state)
		hooks := bond.HookRegistryFromContext(ctx)
		history := append([]bond.Message{}, messages...)
		active := s.entry
		handoffs := 0

		for {
			if ctx.Err() != nil {
				yield(bond.StreamEvent{}, ctx.Err())
				return
			}

			agent, exists := s.agents[active]
			if !exists {
				yield(bond.StreamEvent{}, fmt.Errorf("swarm: unknown agent %q", active))
				return
			}

			// Build options with transfer tools + state tools + agent's own tools.
			opts := agent.Options
			opts.Tools = append(s.buildTools(active), opts.Tools...)

			// Run the agent.
			var textBuf strings.Builder
			var transferTo string

			for event, err := range bond.Stream(ctx, agent.Agent, history, opts) {
				if err != nil {
					yield(bond.StreamEvent{}, err)
					return
				}

				// Check for transfer tool calls in events.
				if event.Type == bond.StreamEventToolUse && event.ToolUse != nil {
					if name, ok := s.isTransferTool(event.ToolUse.Name); ok {
						transferTo = name
					}
				}

				if !yield(event, nil) {
					return
				}

				if event.Type == bond.StreamEventTextDelta {
					textBuf.WriteString(event.TextDelta)
				}
			}

			// Append assistant output to history.
			if textBuf.Len() > 0 {
				history = append(history, bond.Message{
					Role:    bond.RoleAssistant,
					Content: []bond.Block{&bond.TextBlock{Text: textBuf.String()}},
				})
			}

			// If no transfer was requested, we're done.
			if transferTo == "" {
				break
			}

			// Enforce max handoffs.
			handoffs++
			if s.opts.MaxHandoffs > 0 && handoffs >= s.opts.MaxHandoffs {
				break
			}

			// BeforeAgentHandoff — approval gate for swarm transfers.
			if err := hooks.Notify(ctx, &BeforeAgentHandoffHook{
				FromAgent: active,
				ToAgent:   transferTo,
				State:     s.state,
			}); err != nil {
				if errors.Is(err, bond.ErrAbort) {
					break
				}
				yield(bond.StreamEvent{}, fmt.Errorf("swarm: before agent handoff: %w", err))
				return
			}

			// Switch active agent.
			prev := active
			active = transferTo

			// AfterAgentHandoff — observer hook.
			_ = hooks.Notify(ctx, &AfterAgentHandoffHook{
				FromAgent: prev,
				ToAgent:   active,
				State:     s.state,
			})
		}

		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// buildTools creates transfer tools for all agents except the active one,
// plus the shared state tools.
func (s *Swarm) buildTools(active string) []bond.Tool {
	tools := stateTools(s.state)
	for name, sa := range s.agents {
		if name == active {
			continue
		}
		desc := sa.Description
		if desc == "" {
			desc = fmt.Sprintf("Transfer the conversation to the %q agent.", name)
		}
		tools = append(tools, &transferTool{target: name, description: desc})
	}
	return tools
}

// isTransferTool checks if a tool name is a transfer tool and returns
// the target agent name.
func (s *Swarm) isTransferTool(toolName string) (string, bool) {
	const prefix = "transfer_to_"
	if strings.HasPrefix(toolName, prefix) {
		target := strings.TrimPrefix(toolName, prefix)
		if _, exists := s.agents[target]; exists {
			return target, true
		}
	}
	return "", false
}

// transferTool is a tool that signals a handoff to another agent.
type transferTool struct {
	target      string
	description string
}

func (t *transferTool) Name() string        { return "transfer_to_" + t.target }
func (t *transferTool) Description() string { return t.description }
func (t *transferTool) InputSchema() json.Marshaler {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *transferTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	return []bond.Block{&bond.TextBlock{Text: fmt.Sprintf("Transferring to %s...", t.target)}}, nil
}

// Verify interface compliance.
var _ bond.Agent = (*Swarm)(nil)

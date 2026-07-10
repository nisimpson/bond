package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nisimpson/bond"
)

// A2AClient defines the subset of an A2A client needed for delegation.
type A2AClient interface {
	SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) func(yield func(a2a.Event, error) bool)
	SendMessage(ctx context.Context, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error)
}

// AgentOptions configures a Agent.
type AgentOptions struct {
	// Client is the A2A client connected to the server agent.
	Client A2AClient
	// Tools are the local tools the server agent can delegate back to us.
	// Skills are extracted from these and attached to outgoing messages.
	Tools []bond.Tool
}

// Agent is a bond.Agent that communicates with a remote agent
// via A2A, advertising local tools as delegatable skills. When the remote
// agent sends "input required" for a tool call, this agent executes it
// locally and sends the result back — all transparently within Stream.
//
// Example:
//
//	writer := delegation.NewAgent(delegation.AgentOptions{
//	    Client: a2aClient,
//	    Tools:  []bond.Tool{searchTool, calcTool},
//	})
//
//	// Use like any other agent
//	resp, _ := bond.Invoke(ctx, writer, bond.TextPrompt("write about Go"), bond.AgentOptions{})
type Agent struct {
	client    A2AClient
	tools     []bond.Tool
	fulfiller *fulfiller
	skills    []Skill
}

// NewAgent creates a bond.Agent that delegates to a remote agent
// via A2A while handling tool delegation round-trips transparently.
func NewAgent(opts AgentOptions) *Agent {
	return &Agent{
		client:    opts.Client,
		tools:     opts.Tools,
		fulfiller: newFulfiller(opts.Tools...),
		skills:    skillsFromTools(opts.Tools),
	}
}

// Stream implements bond.Agent. It sends the last user message to the remote
// agent with skills attached, handles any "input required" delegation
// round-trips, and yields the final response as stream events.
func (a *Agent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		// Build the A2A message from the last user message.
		msg := a.bondToA2AMessage(messages)

		// Attach our skills to the message.
		if err := attachSkills(msg, a.skills); err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("delegation agent: %w", err))
			return
		}

		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		// Stream from server, handling delegation inline.
		req := &a2a.SendMessageRequest{Message: msg}

		for event, err := range a.client.SendStreamingMessage(ctx, req) {
			if err != nil {
				yield(bond.StreamEvent{}, fmt.Errorf("delegation agent: %w", err))
				return
			}

			switch e := event.(type) {
			case *a2a.TaskStatusUpdateEvent:
				if e.Status.State == a2a.TaskStateInputRequired {
					// Check if this is a delegation tool call.
					toolName, _, _ := parseInputRequired(e)
					if toolName == "" {
						// Not a delegation request — ignore or pass through.
						continue
					}
					// Server needs a tool executed — handle it.
					if err := a.handleInputRequired(ctx, e); err != nil {
						yield(bond.StreamEvent{}, fmt.Errorf("delegation agent: %w", err))
						return
					}
					// Continue watching the stream for more events.
					continue
				}
				if e.Status.State == a2a.TaskStateCompleted {
					// Done — emit stop.
					yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
					return
				}
				if e.Status.State == a2a.TaskStateFailed || e.Status.State == a2a.TaskStateCanceled {
					errMsg := "task " + string(e.Status.State)
					if e.Status.Message != nil {
						for _, p := range e.Status.Message.Parts {
							if text, ok := p.Content.(a2a.Text); ok {
								errMsg = string(text)
							}
						}
					}
					yield(bond.StreamEvent{}, fmt.Errorf("delegation agent: %s", errMsg))
					return
				}

			case *a2a.TaskArtifactUpdateEvent:
				if e.Artifact != nil {
					for _, p := range e.Artifact.Parts {
						if text, ok := p.Content.(a2a.Text); ok {
							if !yield(bond.StreamEvent{
								Type:      bond.StreamEventTextDelta,
								TextDelta: string(text),
							}, nil) {
								return
							}
						}
					}
				}

			case *a2a.Message:
				for _, p := range e.Parts {
					if text, ok := p.Content.(a2a.Text); ok {
						if !yield(bond.StreamEvent{
							Type:      bond.StreamEventTextDelta,
							TextDelta: string(text),
						}, nil) {
							return
						}
					}
				}
			}
		}

		// Stream ended without explicit completion — emit stop.
		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// handleInputRequired executes the requested tool locally and sends the
// result back to the server.
func (a *Agent) handleInputRequired(ctx context.Context, event *a2a.TaskStatusUpdateEvent) error {
	toolName, input, err := parseInputRequired(event)
	if err != nil {
		return err
	}

	blocks, err := a.fulfiller.Execute(ctx, toolName, input)
	if err != nil {
		return fmt.Errorf("fulfill %q: %w", toolName, err)
	}

	var textParts []*a2a.Part
	for _, b := range blocks {
		if tb, ok := b.(*bond.TextBlock); ok {
			textParts = append(textParts, a2a.NewTextPart(tb.Text))
		}
	}

	responseMsg := a2a.NewMessage(a2a.MessageRoleUser, textParts...)
	_, err = a.client.SendMessage(ctx, &a2a.SendMessageRequest{Message: responseMsg})
	return err
}

// bondToA2AMessage converts bond messages to an A2A message (using the last
// user message as the prompt).
func (a *Agent) bondToA2AMessage(messages []bond.Message) *a2a.Message {
	if len(messages) == 0 {
		return a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(""))
	}

	last := messages[len(messages)-1]
	var parts []*a2a.Part
	for _, block := range last.Content {
		if tb, ok := block.(*bond.TextBlock); ok {
			parts = append(parts, a2a.NewTextPart(tb.Text))
		}
	}

	role := a2a.MessageRoleUser
	if last.Role == bond.RoleAssistant {
		role = a2a.MessageRoleAgent
	}

	return a2a.NewMessage(role, parts...)
}

// Verify interface compliance.
var _ bond.Agent = (*Agent)(nil)

// delegationRequestType identifies an "input required" event as a tool delegation.
const delegationRequestType = "delegation:tool_call"

// delegationRequest is the expected shape of the "input required" status message
// for tool delegation. The Type field distinguishes it from other "input required"
// requests (e.g., human-in-the-loop, clarification, etc.).
type delegationRequest struct {
	Type  string          `json:"type"`
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
}

// parseInputRequired extracts tool call information from an "input required" event.
// Returns empty strings if this is not a delegation tool call.
func parseInputRequired(event *a2a.TaskStatusUpdateEvent) (string, json.RawMessage, error) {
	if event.Status.Message == nil {
		return "", nil, fmt.Errorf("input required event has no message")
	}

	// Look for a text part containing a delegation request JSON.
	for _, p := range event.Status.Message.Parts {
		if text, ok := p.Content.(a2a.Text); ok {
			var req delegationRequest
			if err := json.Unmarshal([]byte(text), &req); err == nil && req.Type == delegationRequestType && req.Tool != "" {
				return req.Tool, req.Input, nil
			}
		}
	}

	return "", nil, nil
}

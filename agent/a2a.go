package agent

import (
	"context"
	"encoding/json"
	"io"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nisimpson/helix"
)

// A2AClient defines the subset of A2A protocol client behavior needed
// by A2AAgent. This allows mocking, wrapping, or alternative implementations.
type A2AClient interface {
	SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error]
}

// A2AAgent implements [helix.Agent] over an A2A protocol client. It translates
// helix messages into A2A protocol messages and A2A streaming events back
// into helix StreamEvents.
type A2AAgent struct {
	client A2AClient
}

// NewA2AAgent creates a [helix.Agent] backed by a remote A2A agent.
func NewA2AAgent(client A2AClient) *A2AAgent {
	return &A2AAgent{client: client}
}

// Stream implements [helix.Agent]. It sends the last user message to the
// remote agent via A2A streaming and translates events into helix StreamEvents.
func (a *A2AAgent) Stream(ctx context.Context, messages []helix.Message) iter.Seq2[helix.StreamEvent, error] {
	return func(yield func(helix.StreamEvent, error) bool) {
		// Build A2A message from the last user message.
		a2aMsg := helixToA2AMessage(messages[len(messages)-1])
		req := &a2a.SendMessageRequest{Message: a2aMsg}

		if !yield(helix.StreamEvent{Type: helix.StreamEventStart}, nil) {
			return
		}

		for event, err := range a.client.SendStreamingMessage(ctx, req) {
			if err != nil {
				yield(helix.StreamEvent{}, err)
				return
			}

			streamEvents := a2aEventToStreamEvents(event)
			for _, se := range streamEvents {
				if !yield(se, nil) {
					return
				}
			}
		}

		if !yield(helix.StreamEvent{Type: helix.StreamEventStop, StopReason: helix.StopReasonEnd}, nil) {
			return
		}
	}
}

// helixToA2AMessage converts a [helix.Message] to an A2A protocol message.
func helixToA2AMessage(msg helix.Message) *a2a.Message {
	var parts []*a2a.Part
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *helix.TextBlock:
			parts = append(parts, a2a.NewTextPart(b.Text))
		case *helix.MediaBlock:
			if b.SourceURI != "" {
				parts = append(parts, &a2a.Part{
					Content:   a2a.URL(b.SourceURI),
					MediaType: b.MIMEType,
				})
			} else if b.Source != nil {
				data, _ := io.ReadAll(b.Source)
				parts = append(parts, &a2a.Part{
					Content:   a2a.Raw(data),
					MediaType: b.MIMEType,
				})
			}
		}
	}

	role := a2a.MessageRoleUser
	if msg.Role == helix.RoleAssistant {
		role = a2a.MessageRoleAgent
	}

	return a2a.NewMessage(role, parts...)
}

// a2aEventToStreamEvents translates an A2A event into helix StreamEvents.
func a2aEventToStreamEvents(event a2a.Event) []helix.StreamEvent {
	switch e := event.(type) {
	case *a2a.Message:
		return partsToStreamEvents(e.Parts)
	case *a2a.TaskArtifactUpdateEvent:
		if e.Artifact != nil {
			return partsToStreamEvents(e.Artifact.Parts)
		}
	}
	return nil
}

// partsToStreamEvents converts A2A content parts into helix StreamEvents.
func partsToStreamEvents(parts []*a2a.Part) []helix.StreamEvent {
	var events []helix.StreamEvent
	for _, p := range parts {
		switch c := p.Content.(type) {
		case a2a.Text:
			events = append(events, helix.StreamEvent{
				Type:      helix.StreamEventTextDelta,
				TextDelta: string(c),
			})
		case a2a.Data:
			// Structured data — marshal to JSON text.
			data, err := json.Marshal(c.Value)
			if err == nil {
				events = append(events, helix.StreamEvent{
					Type:      helix.StreamEventTextDelta,
					TextDelta: string(data),
				})
			}
		case a2a.URL:
			// URL content — emit as text for now.
			events = append(events, helix.StreamEvent{
				Type:      helix.StreamEventTextDelta,
				TextDelta: string(c),
			})
		case a2a.Raw:
			events = append(events, helix.StreamEvent{
				Type: helix.StreamEventMediaDelta,
				MediaDelta: &helix.MediaDelta{
					MIMEType: p.MediaType,
					Data:     []byte(c),
				},
			})
		}
	}
	return events
}

// Verify interface compliance.
var _ helix.Agent = (*A2AAgent)(nil)

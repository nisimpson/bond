package agent

import (
	"context"
	"encoding/json"
	"io"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nisimpson/bond"
)

// A2AClient defines the subset of A2A protocol client behavior needed
// by A2AProxy. This allows mocking, wrapping, or alternative implementations.
type A2AClient interface {
	SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error]
}

// A2AProxy implements [bond.Agent] over an A2A protocol client. It translates
// bond messages into A2A protocol messages and A2A streaming events back
// into bond StreamEvents.
type A2AProxy struct {
	client A2AClient
}

// NewA2AProxy creates a [bond.Agent] backed by a remote A2A agent.
func NewA2AProxy(client A2AClient) *A2AProxy {
	return &A2AProxy{client: client}
}

// Stream implements [bond.Agent]. It sends the last user message to the
// remote agent via A2A streaming and translates events into bond StreamEvents.
func (a *A2AProxy) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		// Build A2A message from the last user message.
		a2aMsg := bondToA2AMessage(messages[len(messages)-1])
		req := &a2a.SendMessageRequest{Message: a2aMsg}

		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		for event, err := range a.client.SendStreamingMessage(ctx, req) {
			if err != nil {
				yield(bond.StreamEvent{}, err)
				return
			}

			streamEvents := a2aEventToStreamEvents(event)
			for _, se := range streamEvents {
				if !yield(se, nil) {
					return
				}
			}
		}

		if !yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil) {
			return
		}
	}
}

// bondToA2AMessage converts a [bond.Message] to an A2A protocol message.
func bondToA2AMessage(msg bond.Message) *a2a.Message {
	var parts []*a2a.Part
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *bond.TextBlock:
			parts = append(parts, a2a.NewTextPart(b.Text))
		case *bond.MediaBlock:
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
	if msg.Role == bond.RoleAssistant {
		role = a2a.MessageRoleAgent
	}

	return a2a.NewMessage(role, parts...)
}

// a2aEventToStreamEvents translates an A2A event into bond StreamEvents.
func a2aEventToStreamEvents(event a2a.Event) []bond.StreamEvent {
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

// partsToStreamEvents converts A2A content parts into bond StreamEvents.
func partsToStreamEvents(parts []*a2a.Part) []bond.StreamEvent {
	var events []bond.StreamEvent
	for _, p := range parts {
		switch c := p.Content.(type) {
		case a2a.Text:
			events = append(events, bond.StreamEvent{
				Type:      bond.StreamEventTextDelta,
				TextDelta: string(c),
			})
		case a2a.Data:
			// Structured data — marshal to JSON text.
			data, err := json.Marshal(c.Value)
			if err == nil {
				events = append(events, bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: string(data),
				})
			}
		case a2a.URL:
			// URL content — emit as text for now.
			events = append(events, bond.StreamEvent{
				Type:      bond.StreamEventTextDelta,
				TextDelta: string(c),
			})
		case a2a.Raw:
			events = append(events, bond.StreamEvent{
				Type: bond.StreamEventMediaDelta,
				MediaDelta: &bond.MediaDelta{
					MIMEType: p.MediaType,
					Data:     []byte(c),
				},
			})
		}
	}
	return events
}

// Verify interface compliance.
var _ bond.Agent = (*A2AProxy)(nil)

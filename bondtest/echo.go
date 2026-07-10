package bondtest

import (
	"context"
	"iter"

	"github.com/nisimpson/bond"
)

// EchoAgent is a bond.Agent that echoes back the last user message content.
// Useful for testing pipelines where you need a predictable agent that
// reflects input without transformation.
//
// Example:
//
//	agent := &bondtest.EchoAgent{}
//	resp, _ := bond.Invoke(ctx, agent, bond.TextPrompt("hello"), bond.AgentOptions{})
//	// resp.Text == "hello"
type EchoAgent struct{}

// Stream implements bond.Agent. Extracts text from the last message and
// streams it back as a text delta.
func (e *EchoAgent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		// Extract text from the last message.
		text := extractLastText(messages)

		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}
		if text != "" {
			if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: text}, nil) {
				return
			}
		}
		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// extractLastText pulls text content from the last message in the conversation.
func extractLastText(messages []bond.Message) string {
	if len(messages) == 0 {
		return ""
	}
	last := messages[len(messages)-1]
	var text string
	for _, block := range last.Content {
		if tb, ok := block.(*bond.TextBlock); ok {
			text += tb.Text
		}
	}
	return text
}

var _ bond.Agent = (*EchoAgent)(nil)

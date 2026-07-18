package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	bond "github.com/nisimpson/bond"
)

// pendingTool accumulates JSON fragments for a tool_use content block
// across multiple content_block_delta events.
type pendingTool struct {
	id       string
	name     string
	inputBuf strings.Builder
}

// StreamReader reads Anthropic SSE events from body and yields Bond StreamEvents.
// It takes ownership of body and closes it on return. Anthropic uses named events
// with "event: <type>" lines followed by "data: <json>" lines, unlike the OpenAI
// format which uses only "data:" lines.
func StreamReader(ctx context.Context, body io.ReadCloser, yield func(bond.StreamEvent, error) bool) {
	defer body.Close()

	if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
		return
	}

	var pending *pendingTool
	var stoppedYielded bool
	scanner := bufio.NewScanner(body)

	var eventType string
	for scanner.Scan() {
		if ctx.Err() != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("anthropic: %w", ctx.Err()))
			return
		}

		line := scanner.Text()

		// Parse "event: <type>" lines to determine the event type.
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		// Parse "data: <json>" lines for the payload.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "content_block_start":
			var cbs ContentBlockStart
			if err := json.Unmarshal([]byte(data), &cbs); err != nil {
				yield(bond.StreamEvent{}, fmt.Errorf("anthropic: unmarshal content_block_start: %w", err))
				return
			}
			if cbs.ContentBlock.Type == "tool_use" {
				pending = &pendingTool{id: cbs.ContentBlock.ID, name: cbs.ContentBlock.Name}
			}

		case "content_block_delta":
			var cbd ContentBlockDelta
			if err := json.Unmarshal([]byte(data), &cbd); err != nil {
				yield(bond.StreamEvent{}, fmt.Errorf("anthropic: unmarshal content_block_delta: %w", err))
				return
			}
			switch cbd.Delta.Type {
			case "text_delta":
				if !yield(bond.StreamEvent{Type: bond.StreamEventTextDelta, TextDelta: cbd.Delta.Text}, nil) {
					return
				}
			case "input_json_delta":
				if pending != nil {
					pending.inputBuf.WriteString(cbd.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			if pending != nil {
				if !yield(bond.StreamEvent{
					Type: bond.StreamEventToolUse,
					ToolUse: &bond.ToolUseBlock{
						ID:    pending.id,
						Name:  pending.name,
						Input: json.RawMessage(pending.inputBuf.String()),
					},
				}, nil) {
					return
				}
				pending = nil
			}

		case "message_delta":
			var md MessageDelta
			if err := json.Unmarshal([]byte(data), &md); err != nil {
				yield(bond.StreamEvent{}, fmt.Errorf("anthropic: unmarshal message_delta: %w", err))
				return
			}
			reason := mapStopReason(md.Delta.StopReason)
			stoppedYielded = true
			yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: reason}, nil)
			return

		case "message_stop":
			if !stoppedYielded {
				yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
			}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		yield(bond.StreamEvent{}, fmt.Errorf("anthropic: read stream: %w", err))
	}
}

// mapStopReason converts an Anthropic stop_reason string to a Bond [bond.StopReason].
func mapStopReason(reason string) bond.StopReason {
	switch reason {
	case "tool_use":
		return bond.StopReasonToolUse
	case "max_tokens":
		return bond.StopReasonLength
	default:
		return bond.StopReasonEnd
	}
}

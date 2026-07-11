// Requirement: 4.4, 4.5, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 6.3, 6.4, 6.5 — SSE parsing and tool accumulation
package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	bond "github.com/nisimpson/bond"
)

// pendingToolCall accumulates fragments for a single tool call.
type pendingToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

// StreamReader reads SSE lines from the response body and yields Bond StreamEvents.
// It handles text deltas, tool call accumulation, and stop reason mapping.
// The errPrefix is prepended to all errors (e.g., "ollama:" or "openai:").
func StreamReader(ctx context.Context, body io.ReadCloser, errPrefix string, yield func(bond.StreamEvent, error) bool) {
	// Requirement: 6.4 — ensure body is closed on exit
	defer body.Close()

	// Requirement: 5.1 — yield start event before processing any chunks
	if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
		return
	}

	var pendingToolCalls []*pendingToolCall
	var lastFinishReason string

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		// Requirement: 6.4 — handle context cancellation during scanning
		if ctx.Err() != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("%s %w", errPrefix, ctx.Err()))
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Requirement: 4.4 — parse data: lines from SSE stream
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		// Requirement: 4.5 — handle [DONE] sentinel as stream completion
		if payload == "[DONE]" {
			break
		}

		// Requirement: 6.3 — handle malformed JSON with prefixed error messages
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("%s unmarshal chunk: %w", errPrefix, err))
			return
		}

		for _, choice := range chunk.Choices {
			// Requirement: 5.2 — yield text delta events for content
			if choice.Delta.Content != "" {
				if !yield(bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: choice.Delta.Content,
				}, nil) {
					return
				}
			}

			// Requirement: 5.3 — accumulate tool call fragments by index
			for _, toolDelta := range choice.Delta.ToolCalls {
				for toolDelta.Index >= len(pendingToolCalls) {
					pendingToolCalls = append(pendingToolCalls, &pendingToolCall{})
				}
				if toolDelta.ID != "" {
					pendingToolCalls[toolDelta.Index].id = toolDelta.ID
				}
				if toolDelta.Function.Name != "" {
					pendingToolCalls[toolDelta.Index].name += toolDelta.Function.Name
				}
				if toolDelta.Function.Arguments != "" {
					pendingToolCalls[toolDelta.Index].arguments.WriteString(toolDelta.Function.Arguments)
				}
			}

			// Requirement: 5.5, 5.6, 5.7 — map finish_reason and flush
			if choice.FinishReason != nil {
				lastFinishReason = *choice.FinishReason
				goto flush
			}
		}
	}

	// Check for scanner errors (e.g., read failures on the body)
	if err := scanner.Err(); err != nil {
		yield(bond.StreamEvent{}, fmt.Errorf("%s read stream: %w", errPrefix, err))
		return
	}

flush:
	// Requirement: 5.4 — flush accumulated tool calls as StreamEventToolUse events
	for _, tc := range pendingToolCalls {
		if tc.name == "" {
			continue
		}
		if !yield(bond.StreamEvent{
			Type: bond.StreamEventToolUse,
			ToolUse: &bond.ToolUseBlock{
				ID:    tc.id,
				Name:  tc.name,
				Input: json.RawMessage(tc.arguments.String()),
			},
		}, nil) {
			return
		}
	}

	// Requirement: 5.5, 5.6, 5.7 — map finish_reason to Bond StopReason
	reason := mapStopReason(lastFinishReason)
	yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: reason}, nil)
}

// mapStopReason converts an OpenAI finish_reason string to a Bond StopReason.
func mapStopReason(reason string) bond.StopReason {
	// Requirement: 5.5, 5.6, 5.7 — stop reason mapping
	switch reason {
	case "tool_calls":
		return bond.StopReasonToolUse
	case "length":
		return bond.StopReasonLength
	default:
		return bond.StopReasonEnd
	}
}

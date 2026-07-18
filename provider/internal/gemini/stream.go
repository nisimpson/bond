// Requirement: 9.1, 9.2, 9.3, 9.4, 9.5, 9.6, 9.7, 9.8 — Gemini SSE parsing and event mapping
package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	bond "github.com/nisimpson/bond"
)

// StreamReader reads Gemini SSE events from body and yields Bond StreamEvents.
// It takes ownership of body and closes it on return. Gemini uses plain "data: <json>"
// lines without named event types or a [DONE] sentinel, unlike OpenAI and Anthropic.
func StreamReader(ctx context.Context, body io.ReadCloser, yield func(bond.StreamEvent, error) bool) {
	defer body.Close()

	if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
		return
	}

	var hadFunctionCall bool
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		if ctx.Err() != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: %w", ctx.Err()))
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var resp StreamResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: unmarshal response: %w", err))
			return
		}

		if len(resp.Candidates) == 0 {
			continue
		}
		candidate := resp.Candidates[0]

		// Process content parts in order.
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					if !yield(bond.StreamEvent{
						Type:      bond.StreamEventTextDelta,
						TextDelta: part.Text,
					}, nil) {
						return
					}
				}
				if part.FunctionCall != nil {
					hadFunctionCall = true
					if !yield(bond.StreamEvent{
						Type: bond.StreamEventToolUse,
						ToolUse: &bond.ToolUseBlock{
							ID:    uuid.NewString(),
							Name:  part.FunctionCall.Name,
							Input: part.FunctionCall.Args,
						},
					}, nil) {
						return
					}
				}
			}
		}

		// Handle explicit finish reason from the candidate.
		if candidate.FinishReason != "" {
			reason := mapFinishReason(candidate.FinishReason)
			yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: reason}, nil)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		yield(bond.StreamEvent{}, fmt.Errorf("gemini: read stream: %w", err))
		return
	}

	// Stream ended without explicit finish reason — infer stop reason.
	if hadFunctionCall {
		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonToolUse}, nil)
		return
	}
	yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
}

// mapFinishReason converts a Gemini finishReason string to a Bond [bond.StopReason].
func mapFinishReason(reason string) bond.StopReason {
	switch reason {
	case "MAX_TOKENS":
		return bond.StopReasonLength
	case "STOP":
		return bond.StopReasonEnd
	default:
		// SAFETY, RECITATION, etc.
		return bond.StopReasonEnd
	}
}

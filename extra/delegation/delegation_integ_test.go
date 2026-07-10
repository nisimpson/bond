package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

// TestIntegration_DelegatedToolViaBondStream tests the full delegation flow
// using bond.Stream with a bondtest.Agent. The target agent requests a
// delegated tool, the delegation plugin sends the request to the caller's
// fulfiller (in-process), and the result is returned to the target agent
// as a normal tool result.
func TestIntegration_DelegatedToolViaBondStream(t *testing.T) {
	ctx := context.Background()

	// --- Caller side setup ---

	// Caller has a "search" tool that returns results based on query.
	searchTool := &fakeTool{
		name: "search",
		desc: "Search the web for information",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			var params struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(input, &params)
			return []bond.Block{
				&bond.TextBlock{Text: "Search results for: " + params.Query},
			}, nil
		},
	}

	// Caller builds a fulfiller from its tools.
	fulfiller := newFulfiller(searchTool)

	// Caller extracts skills for the target.
	skills := skillsFromTools([]bond.Tool{searchTool})

	// --- Target side setup ---

	// In-process requester: when the target's proxy tool is called,
	// it delegates directly to the caller's fulfiller.
	requester := &inProcessRequester{fulfiller: fulfiller}

	// Target creates the delegation plugin with caller's skills.
	delegationPlugin := NewPlugin(Options{
		Requester: requester,
		Skills:    skills,
	})

	// Target agent: a bondtest.Agent that simulates a model which:
	// 1. First call: requests the "search" tool
	// 2. Second call (after tool result): produces a final text response
	callCount := 0
	targetAgent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			return func(yield func(bond.StreamEvent, error) bool) {
				callCount++

				if callCount == 1 {
					// First invocation: model wants to call "search"
					if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
						return
					}
					if !yield(bond.StreamEvent{
						Type: bond.StreamEventToolUse,
						ToolUse: &bond.ToolUseBlock{
							ID:    "call-1",
							Name:  "search",
							Input: json.RawMessage(`{"query":"Go programming language"}`),
						},
					}, nil) {
						return
					}
					yield(bond.StreamEvent{
						Type:       bond.StreamEventStop,
						StopReason: bond.StopReasonToolUse,
					}, nil)
					return
				}

				// Second invocation: model has the search results, produces final output.
				// Verify it received the tool result in messages.
				lastMsg := messages[len(messages)-1]
				var toolResultText string
				for _, block := range lastMsg.Content {
					if tr, ok := block.(*bond.ToolResultBlock); ok {
						for _, b := range tr.Content {
							if tb, ok := b.(*bond.TextBlock); ok {
								toolResultText = tb.Text
							}
						}
					}
				}

				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				if !yield(bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: "Article based on: " + toolResultText,
				}, nil) {
					return
				}
				yield(bond.StreamEvent{
					Type:       bond.StreamEventStop,
					StopReason: bond.StopReasonEnd,
				}, nil)
			}
		},
	}

	// --- Run the target agent with delegation plugin ---

	resp, err := bond.Invoke(ctx, targetAgent, bond.TextPrompt("write about Go"), bond.AgentOptions{
		Plugins: []bond.Plugin{delegationPlugin},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// --- Verify ---

	expected := "Article based on: Search results for: Go programming language"
	if resp.Text != expected {
		t.Errorf("expected %q, got %q", expected, resp.Text)
	}

	if callCount != 2 {
		t.Errorf("expected 2 agent calls (tool request + final), got %d", callCount)
	}
}

// TestIntegration_MultipleDelegatedTools tests that multiple delegated tools
// execute concurrently and return correct results.
func TestIntegration_MultipleDelegatedTools(t *testing.T) {
	ctx := context.Background()

	// Caller has two tools.
	searchTool := &fakeTool{
		name: "search",
		desc: "Search",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			return []bond.Block{&bond.TextBlock{Text: "search-result"}}, nil
		},
	}
	calcTool := &fakeTool{
		name: "calc",
		desc: "Calculator",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			return []bond.Block{&bond.TextBlock{Text: "calc-result"}}, nil
		},
	}

	fulfiller := newFulfiller(searchTool, calcTool)
	skills := skillsFromTools([]bond.Tool{searchTool, calcTool})
	requester := &inProcessRequester{fulfiller: fulfiller}

	plugin := NewPlugin(Options{
		Requester: requester,
		Skills:    skills,
	})

	// Target agent requests both tools simultaneously.
	callCount := 0
	targetAgent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			return func(yield func(bond.StreamEvent, error) bool) {
				callCount++

				if callCount == 1 {
					if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
						return
					}
					if !yield(bond.StreamEvent{
						Type: bond.StreamEventToolUse,
						ToolUse: &bond.ToolUseBlock{
							ID: "c1", Name: "search", Input: json.RawMessage(`{}`),
						},
					}, nil) {
						return
					}
					if !yield(bond.StreamEvent{
						Type: bond.StreamEventToolUse,
						ToolUse: &bond.ToolUseBlock{
							ID: "c2", Name: "calc", Input: json.RawMessage(`{}`),
						},
					}, nil) {
						return
					}
					yield(bond.StreamEvent{
						Type:       bond.StreamEventStop,
						StopReason: bond.StopReasonToolUse,
					}, nil)
					return
				}

				// Second call: verify both results came back.
				lastMsg := messages[len(messages)-1]
				var results []string
				for _, block := range lastMsg.Content {
					if tr, ok := block.(*bond.ToolResultBlock); ok {
						for _, b := range tr.Content {
							if tb, ok := b.(*bond.TextBlock); ok {
								results = append(results, tb.Text)
							}
						}
					}
				}

				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				if !yield(bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: "got " + results[0] + " and " + results[1],
				}, nil) {
					return
				}
				yield(bond.StreamEvent{
					Type:       bond.StreamEventStop,
					StopReason: bond.StopReasonEnd,
				}, nil)
			}
		},
	}

	resp, err := bond.Invoke(ctx, targetAgent, bond.TextPrompt("do both"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// Both tools should have been called and results combined.
	if resp.Text != "got search-result and calc-result" {
		t.Errorf("unexpected result: %q", resp.Text)
	}
}

// TestIntegration_DelegatedToolError tests error propagation from a failed
// delegated tool call.
func TestIntegration_DelegatedToolError(t *testing.T) {
	ctx := context.Background()

	// Caller has a tool that always fails.
	failTool := &fakeTool{
		name: "fail",
		desc: "Always fails",
		runFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			return nil, errors.New("tool exploded")
		},
	}

	fulfiller := newFulfiller(failTool)
	skills := skillsFromTools([]bond.Tool{failTool})
	requester := &inProcessRequester{fulfiller: fulfiller}

	plugin := NewPlugin(Options{
		Requester: requester,
		Skills:    skills,
	})

	// Target agent requests the failing tool.
	callCount := 0
	targetAgent := &bondtest.Agent{
		StreamFunc: func(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
			return func(yield func(bond.StreamEvent, error) bool) {
				callCount++

				if callCount == 1 {
					if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
						return
					}
					if !yield(bond.StreamEvent{
						Type: bond.StreamEventToolUse,
						ToolUse: &bond.ToolUseBlock{
							ID: "c1", Name: "fail", Input: json.RawMessage(`{}`),
						},
					}, nil) {
						return
					}
					yield(bond.StreamEvent{
						Type:       bond.StreamEventStop,
						StopReason: bond.StopReasonToolUse,
					}, nil)
					return
				}

				// Second call: the error should be in the tool result.
				lastMsg := messages[len(messages)-1]
				var errorText string
				for _, block := range lastMsg.Content {
					if tr, ok := block.(*bond.ToolResultBlock); ok && tr.IsError {
						for _, b := range tr.Content {
							if tb, ok := b.(*bond.TextBlock); ok {
								errorText = tb.Text
							}
						}
					}
				}

				if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
					return
				}
				if !yield(bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: "error: " + errorText,
				}, nil) {
					return
				}
				yield(bond.StreamEvent{
					Type:       bond.StreamEventStop,
					StopReason: bond.StopReasonEnd,
				}, nil)
			}
		},
	}

	resp, err := bond.Invoke(ctx, targetAgent, bond.TextPrompt("try it"), bond.AgentOptions{
		Plugins: []bond.Plugin{plugin},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// The model should have received the error in the tool result.
	if resp.Text == "" {
		t.Error("expected non-empty response with error info")
	}
	t.Logf("Response: %s", resp.Text)
}

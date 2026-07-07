package helix

import (
	"context"
	"errors"
	"iter"
	"sync"
)

type JSONMarshaler = func(any) ([]byte, error)

// StreamEventType identifies what kind of event was emitted during streaming.
type StreamEventType string

const (
	// StreamEventStart signals the beginning of the response stream.
	StreamEventStart StreamEventType = "start"
	// StreamEventTextDelta delivers an incremental chunk of text.
	StreamEventTextDelta StreamEventType = "text_delta"
	// StreamEventMediaDelta delivers a chunk of binary/media content.
	StreamEventMediaDelta StreamEventType = "media_delta"
	// StreamEventToolUse signals the model wants to invoke a tool.
	StreamEventToolUse StreamEventType = "tool_use"
	// StreamEventStop signals the response is complete.
	StreamEventStop StreamEventType = "stop"
)

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEnd     StopReason = "end"      // natural end of response
	StopReasonToolUse StopReason = "tool_use" // paused to call a tool
	StopReasonLength  StopReason = "length"   // hit max token limit
)

// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
	Type StreamEventType

	// TextDelta is populated when Type == StreamEventTextDelta.
	TextDelta string

	// MediaDelta is populated when Type == StreamEventMediaDelta.
	MediaDelta *MediaDelta

	// ToolUse is populated when Type == StreamEventToolUse.
	ToolUse *ToolUseBlock

	// StopReason is populated when Type == StreamEventStop.
	StopReason StopReason

	// Metadata carries provider-specific event data (usage stats, trace IDs, etc.)
	Metadata map[string]any
}

// MediaDelta represents a chunk of binary/media content in a stream.
type MediaDelta struct {
	// MIMEType is the media type (e.g. "image/png", "audio/wav").
	MIMEType string
	// Data is the raw bytes for this chunk.
	Data []byte
}

// Agent is the core interface for streaming LLM interactions.
type Agent interface {
	// Stream sends the conversation messages to the model and returns
	// an iterator of streaming events. The last message in the slice
	// is treated as the current prompt; preceding messages are context.
	Stream(ctx context.Context, messages []Message) iter.Seq2[StreamEvent, error]
}

// StreamOptions configures the agent loop.
type StreamOptions struct {
	Tools    []Tool
	Hooks    *HookRegistry
	Plugins  []Plugin
	MaxTurns int // max tool-use round-trips; 0 means unlimited
}

// streamLoop holds the state for a single Stream invocation.
type streamLoop struct {
	agent   Agent
	tools   map[string]Tool
	hooks   *HookRegistry
	history []Message
	opts    StreamOptions
	turns   int
}

// notify dispatches a hook event. No-ops if the registry is nil.
func (l *streamLoop) notify(ctx context.Context, event HookEvent) error {
	if l.hooks == nil {
		return nil
	}
	return l.hooks.Notify(ctx, event)
}

// Stream runs the full agent loop: it streams from the provider, and when the
// model requests tool use, it executes the tools, appends results to the
// conversation, and re-invokes the provider. Events are yielded to the caller
// transparently across turns.
func Stream(ctx context.Context, agent Agent, messages []Message, opts StreamOptions) iter.Seq2[StreamEvent, error] {
	// Ensure we have a registry (plugins and hooks need one).
	if opts.Hooks == nil {
		opts.Hooks = &HookRegistry{}
	}

	// Collect tools from options and plugins.
	allTools := append([]Tool{}, opts.Tools...)
	for _, p := range opts.Plugins {
		allTools = append(allTools, p.Tools()...)
		p.Init(opts.Hooks)
	}

	loop := &streamLoop{
		agent:   agent,
		tools:   indexTools(allTools),
		hooks:   opts.Hooks,
		history: append([]Message{}, messages...),
		opts:    opts,
	}
	return loop.run(ctx)
}

// indexTools builds a name-keyed lookup map from a tool slice.
func indexTools(tools []Tool) map[string]Tool {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return m
}

// run is the top-level iterator that drives the agent loop.
func (l *streamLoop) run(ctx context.Context) iter.Seq2[StreamEvent, error] {
	return func(yield func(StreamEvent, error) bool) {
		// BeforeStream
		if err := l.notify(ctx, &BeforeStreamHook{Messages: l.history}); err != nil {
			if errors.Is(err, ErrAbort) {
				return
			}
			yield(StreamEvent{}, err)
			return
		}

		for {
			if ctx.Err() != nil {
				yield(StreamEvent{}, ctx.Err())
				return
			}

			// BeforeModelInvoke
			if err := l.notify(ctx, &BeforeModelInvokeHook{Messages: l.history}); err != nil {
				if errors.Is(err, ErrAbort) {
					return
				}
				yield(StreamEvent{}, err)
				return
			}

			blocks, stopReason, ok := l.consumeStream(ctx, yield)
			if !ok {
				return
			}

			// AfterModelInvoke
			l.notify(ctx, &AfterModelInvokeHook{Blocks: blocks, StopReason: stopReason})

			assistantMsg := Message{Role: RoleAssistant, Content: blocks}
			l.history = append(l.history, assistantMsg)
			l.notify(ctx, &OnMessageAppendedHook{Message: assistantMsg})

			if stopReason != StopReasonToolUse {
				l.notify(ctx, &AfterStreamHook{Messages: l.history})
				return
			}

			if l.maxTurnsReached() {
				l.notify(ctx, &AfterStreamHook{Messages: l.history})
				return
			}

			results := l.executeTools(ctx, blocks)

			toolMsg := Message{Role: RoleUser, Content: results}
			l.history = append(l.history, toolMsg)
			l.notify(ctx, &OnMessageAppendedHook{Message: toolMsg})
		}
	}
}

// consumeStream reads all events from a single provider invocation, forwarding
// them to the caller and accumulating the assistant's content blocks. Returns
// false for ok if the yield was cancelled or an error occurred.
func (l *streamLoop) consumeStream(ctx context.Context, yield func(StreamEvent, error) bool) ([]Block, StopReason, bool) {
	var textBuf string
	var blocks []Block
	var stopReason StopReason

	for event, err := range l.agent.Stream(ctx, l.history) {
		if err != nil {
			yield(StreamEvent{}, err)
			return nil, "", false
		}

		// OnStreamEvent
		l.notify(ctx, &OnStreamEventHook{Event: event})

		if !yield(event, nil) {
			return nil, "", false
		}

		switch event.Type {
		case StreamEventTextDelta:
			textBuf += event.TextDelta
		case StreamEventToolUse:
			if textBuf != "" {
				blocks = append(blocks, &TextBlock{Text: textBuf})
				textBuf = ""
			}
			if event.ToolUse != nil {
				blocks = append(blocks, event.ToolUse)
			}
		case StreamEventStop:
			stopReason = event.StopReason
		}
	}

	if textBuf != "" {
		blocks = append(blocks, &TextBlock{Text: textBuf})
	}

	return blocks, stopReason, true
}

// maxTurnsReached increments the turn counter and reports whether the
// configured limit has been hit.
func (l *streamLoop) maxTurnsReached() bool {
	l.turns++
	return l.opts.MaxTurns > 0 && l.turns >= l.opts.MaxTurns
}

// executeTools runs all tool use blocks concurrently and returns results
// in the same order they were requested.
func (l *streamLoop) executeTools(ctx context.Context, blocks []Block) []Block {
	var toolCalls []*ToolUseBlock
	for _, b := range blocks {
		if tu, ok := b.(*ToolUseBlock); ok {
			toolCalls = append(toolCalls, tu)
		}
	}

	// BeforeToolCycle
	l.notify(ctx, &BeforeToolCycleHook{ToolCalls: toolCalls})

	results := make([]*ToolResultBlock, len(toolCalls))
	var wg sync.WaitGroup

	for i, tu := range toolCalls {
		wg.Add(1)
		go func(i int, tu *ToolUseBlock) {
			defer wg.Done()
			results[i] = l.runTool(ctx, tu)
		}(i, tu)
	}
	wg.Wait()

	// AfterToolCycle
	l.notify(ctx, &AfterToolCycleHook{Results: results})

	out := make([]Block, len(results))
	for i, r := range results {
		out[i] = r
	}
	return out
}

// runTool executes a single tool call and returns the result block.
func (l *streamLoop) runTool(ctx context.Context, tu *ToolUseBlock) *ToolResultBlock {
	// BeforeToolCall — abort skips this tool
	if err := l.notify(ctx, &BeforeToolCallHook{ToolUse: tu}); errors.Is(err, ErrAbort) {
		return &ToolResultBlock{
			ToolUseID: tu.ID,
			Content:   []Block{&TextBlock{Text: "error: tool call aborted by hook"}},
			IsError:   true,
		}
	}

	tool, exists := l.tools[tu.Name]
	if !exists {
		result := &ToolResultBlock{
			ToolUseID: tu.ID,
			Content:   []Block{&TextBlock{Text: "error: unknown tool: " + tu.Name}},
			IsError:   true,
		}
		l.notify(ctx, &AfterToolCallHook{ToolUse: tu, Result: result})
		return result
	}

	output, err := tool.Run(ctx, tu.Input)
	if err != nil {
		result := &ToolResultBlock{
			ToolUseID: tu.ID,
			Content:   []Block{&TextBlock{Text: "error: " + err.Error()}},
			IsError:   true,
		}
		l.notify(ctx, &AfterToolCallHook{ToolUse: tu, Result: result})
		return result
	}

	result := &ToolResultBlock{
		ToolUseID: tu.ID,
		Content:   output,
	}
	l.notify(ctx, &AfterToolCallHook{ToolUse: tu, Result: result})
	return result
}

// TextPrompt is a convenience helper that wraps a plain string into
// the []Message format expected by Agent.Stream.
func TextPrompt(text string) []Message {
	return []Message{
		{
			Role:    RoleUser,
			Content: []Block{&TextBlock{Text: text}},
		},
	}
}

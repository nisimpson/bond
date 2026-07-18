// Package slogger provides a structured logging plugin for bond agents.
// It logs agent lifecycle events (stream start/end, model invocations,
// tool calls) using [log/slog]. The plugin implements [bond.ContextPlugin]
// to inject a request-scoped logger into context, making it available to
// hooks, tools, and downstream code via [FromContext].
package slogger

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nisimpson/bond"
)

// loggerContextKey is the context key for the request-scoped logger.
type loggerContextKey struct{}

// FromContext retrieves the request-scoped [*slog.Logger] from the context.
// Returns [slog.Default] if no logger was injected (i.e., plugin not active).
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Options configures the slogger plugin.
type Options struct {
	// Logger is the base logger. If nil, [slog.Default] is used.
	// To add request-scoped attributes, pass a child logger:
	//   slogger.NewPlugin(slogger.Options{Logger: logger.With("request_id", id)})
	Logger *slog.Logger
	// Level is the log level for normal lifecycle messages. Default: [slog.LevelInfo].
	Level slog.Level
	// ErrorLevel is the log level used when a tool call results in an error.
	// Default (zero value): [slog.LevelInfo].
	ErrorLevel slog.Level
}

// Plugin is the structured logging plugin for bond agents.
type Plugin struct {
	logger     *slog.Logger
	level      slog.Level
	errorLevel slog.Level

	streamStart time.Time

	mu         sync.Mutex
	toolStarts map[string]time.Time // keyed by call ID
}

// NewPlugin creates a slogger plugin with the given options.
func NewPlugin(opts Options) *Plugin {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Plugin{
		logger:     logger,
		level:      opts.Level,
		errorLevel: opts.ErrorLevel,
		toolStarts: make(map[string]time.Time),
	}
}

// Name implements [bond.Plugin].
func (p *Plugin) Name() string { return "slogger" }

// Tools implements [bond.Plugin]. The slogger plugin contributes no tools.
func (p *Plugin) Tools() []bond.Tool { return nil }

// Init implements [bond.Plugin]. Registers logging hooks.
func (p *Plugin) Init(registry *bond.HookRegistry) {
	bond.OnBefore(registry, p.beforeStream)
	bond.OnAfter(registry, p.afterStream)
	bond.OnBefore(registry, p.beforeModelInvoke)
	bond.OnAfter(registry, p.afterModelInvoke)
	bond.OnBefore(registry, p.beforeToolCall)
	bond.OnAfter(registry, p.afterToolCall)
}

// InitContext implements [bond.ContextPlugin]. Injects a request-scoped logger
// into the context with any configured attributes.
func (p *Plugin) InitContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, loggerContextKey{}, p.logger)
}

func (p *Plugin) beforeStream(ctx context.Context, hook *bond.BeforeStreamHook) error {
	p.streamStart = time.Now()
	FromContext(ctx).Log(ctx, p.level, "stream.start",
		slog.Int("message_count", len(hook.Messages)),
	)
	return nil
}

func (p *Plugin) afterStream(ctx context.Context, hook *bond.AfterStreamHook) {
	FromContext(ctx).Log(ctx, p.level, "stream.end",
		slog.Int("message_count", len(hook.Messages)),
		slog.Duration("duration", time.Since(p.streamStart)),
	)
}

func (p *Plugin) beforeModelInvoke(ctx context.Context, hook *bond.BeforeModelInvokeHook) error {
	FromContext(ctx).Log(ctx, p.level, "model.invoke",
		slog.Int("attempt", hook.Attempt),
		slog.Int("message_count", len(hook.Messages)),
	)
	return nil
}

func (p *Plugin) afterModelInvoke(ctx context.Context, hook *bond.AfterModelInvokeHook) {
	FromContext(ctx).Log(ctx, p.level, "model.response",
		slog.String("stop_reason", string(hook.StopReason)),
		slog.Int("block_count", len(hook.Blocks)),
	)
}

func (p *Plugin) beforeToolCall(ctx context.Context, hook *bond.BeforeToolCallHook) error {
	p.mu.Lock()
	p.toolStarts[hook.ToolUse.ID] = time.Now()
	p.mu.Unlock()

	FromContext(ctx).Log(ctx, p.level, "tool.call.start",
		slog.String("tool", hook.ToolUse.Name),
		slog.String("call_id", hook.ToolUse.ID),
	)
	return nil
}

func (p *Plugin) afterToolCall(ctx context.Context, hook *bond.AfterToolCallHook) {
	p.mu.Lock()
	start, ok := p.toolStarts[hook.ToolUse.ID]
	if ok {
		delete(p.toolStarts, hook.ToolUse.ID)
	}
	p.mu.Unlock()

	var duration time.Duration
	if ok {
		duration = time.Since(start)
	}

	level := p.level
	if hook.Result.IsError {
		level = p.errorLevel
	}

	FromContext(ctx).Log(ctx, level, "tool.call.end",
		slog.String("tool", hook.ToolUse.Name),
		slog.String("call_id", hook.ToolUse.ID),
		slog.Bool("is_error", hook.Result.IsError),
		slog.Duration("duration", duration),
	)
}

// Verify interface compliance.
var _ bond.Plugin = (*Plugin)(nil)
var _ bond.ContextPlugin = (*Plugin)(nil)

package otel

import (
	"context"
	"fmt"

	"github.com/nisimpson/bond"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const instrumentationName = "github.com/nisimpson/bond/extra/otel"

// PluginOptions configures the OpenTelemetry plugin.
type PluginOptions struct {
	// TracerProvider supplies the tracer. If nil, a no-op provider is used.
	TracerProvider trace.TracerProvider
	// SpanNamePrefix is prepended to span names. Defaults to "bond".
	SpanNamePrefix string
}

// Plugin is the OpenTelemetry observability plugin for bond agents.
type Plugin struct {
	tracer trace.Tracer
	prefix string

	// spans tracks active spans keyed by lifecycle phase.
	streamSpan trace.Span
	modelSpan  trace.Span
	toolSpans  map[string]trace.Span
}

// NewPlugin creates an OpenTelemetry plugin with the given options.
func NewPlugin(opts PluginOptions) *Plugin {
	if opts.SpanNamePrefix == "" {
		opts.SpanNamePrefix = "bond"
	}

	var tracer trace.Tracer
	if opts.TracerProvider != nil {
		tracer = opts.TracerProvider.Tracer(instrumentationName)
	} else {
		tracer = tracenoop.NewTracerProvider().Tracer(instrumentationName)
	}

	return &Plugin{
		tracer:    tracer,
		prefix:    opts.SpanNamePrefix,
		toolSpans: make(map[string]trace.Span),
	}
}

// Name implements [bond.Plugin].
func (p *Plugin) Name() string { return "otel" }

// Tools implements [bond.Plugin]. The otel plugin contributes no tools.
func (p *Plugin) Tools() []bond.Tool { return nil }

// Init implements [bond.Plugin]. Registers tracing hooks.
func (p *Plugin) Init(registry *bond.HookRegistry) {
	p.initStreamHooks(registry)
	p.initModelHooks(registry)
	p.initToolHooks(registry)
}

func (p *Plugin) initStreamHooks(registry *bond.HookRegistry) {
	bond.OnBefore(registry, func(ctx context.Context, hook *bond.BeforeStreamHook) error {
		_, span := p.tracer.Start(ctx, p.prefix+".stream",
			trace.WithAttributes(
				attribute.Int("bond.message_count", len(hook.Messages)),
			),
		)
		p.streamSpan = span
		return nil
	})

	bond.OnAfter(registry, func(_ context.Context, hook *bond.AfterStreamHook) {
		if p.streamSpan != nil {
			p.streamSpan.SetAttributes(
				attribute.Int("bond.final_message_count", len(hook.Messages)),
			)
			p.streamSpan.End()
			p.streamSpan = nil
		}
	})
}

func (p *Plugin) initModelHooks(registry *bond.HookRegistry) {
	bond.OnBefore(registry, func(ctx context.Context, hook *bond.BeforeModelInvokeHook) error {
		_, span := p.tracer.Start(ctx, p.prefix+".model_invoke",
			trace.WithAttributes(
				attribute.Int("bond.attempt", hook.Attempt),
				attribute.Int("bond.message_count", len(hook.Messages)),
			),
		)
		p.modelSpan = span
		return nil
	})

	bond.OnAfter(registry, func(_ context.Context, hook *bond.AfterModelInvokeHook) {
		if p.modelSpan != nil {
			p.modelSpan.SetAttributes(
				attribute.String("bond.stop_reason", string(hook.StopReason)),
				attribute.Int("bond.block_count", len(hook.Blocks)),
			)
			p.modelSpan.End()
			p.modelSpan = nil
		}
	})
}

func (p *Plugin) initToolHooks(registry *bond.HookRegistry) {
	bond.OnBefore(registry, func(ctx context.Context, hook *bond.BeforeToolCallHook) error {
		_, span := p.tracer.Start(ctx, p.prefix+".tool."+hook.ToolUse.Name,
			trace.WithAttributes(
				attribute.String("bond.tool.name", hook.ToolUse.Name),
				attribute.String("bond.tool.id", hook.ToolUse.ID),
			),
		)
		p.toolSpans[hook.ToolUse.ID] = span
		return nil
	})

	bond.OnAfter(registry, func(_ context.Context, hook *bond.AfterToolCallHook) {
		span, ok := p.toolSpans[hook.ToolUse.ID]
		if !ok {
			return
		}
		delete(p.toolSpans, hook.ToolUse.ID)

		span.SetAttributes(
			attribute.Bool("bond.tool.is_error", hook.Result.IsError),
		)

		if hook.Result.IsError {
			span.SetStatus(codes.Error, fmt.Sprintf("tool %s failed", hook.ToolUse.Name))
		}

		span.End()
	})
}

// Verify interface compliance.
var _ bond.Plugin = (*Plugin)(nil)

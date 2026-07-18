package otel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nisimpson/bond"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const instrumentationName = "github.com/nisimpson/bond/extra/otel"

// PluginOptions configures the OpenTelemetry plugin.
type PluginOptions struct {
	// TracerProvider supplies the tracer. If nil, a no-op provider is used.
	TracerProvider trace.TracerProvider
	// MeterProvider supplies the meter for metrics. If nil, a no-op provider is used.
	MeterProvider metric.MeterProvider
	// SpanNamePrefix is prepended to span names. Defaults to "bond".
	SpanNamePrefix string
}

// Plugin is the OpenTelemetry observability plugin for bond agents.
// It emits both traces (spans) and metrics (counters, histograms) for
// agent lifecycle events.
type Plugin struct {
	tracer trace.Tracer
	prefix string

	// Trace state
	streamSpan trace.Span
	modelSpan  trace.Span
	toolSpans  map[string]trace.Span

	// Metric instruments
	streamDuration   metric.Float64Histogram
	modelInvocations metric.Int64Counter
	toolCalls        metric.Int64Counter
	toolDuration     metric.Float64Histogram

	// Timing state for metrics
	streamStart time.Time

	// mu protects toolStarts for concurrent tool calls.
	mu         sync.Mutex
	toolStarts map[string]time.Time
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

	var meter metric.Meter
	if opts.MeterProvider != nil {
		meter = opts.MeterProvider.Meter(instrumentationName)
	} else {
		meter = metricnoop.NewMeterProvider().Meter(instrumentationName)
	}

	streamDuration, _ := meter.Float64Histogram("bond.stream.duration",
		metric.WithDescription("Duration of agent streams in seconds"),
		metric.WithUnit("s"),
	)

	modelInvocations, _ := meter.Int64Counter("bond.model.invocations",
		metric.WithDescription("Number of model invocations"),
	)

	toolCalls, _ := meter.Int64Counter("bond.tool.calls",
		metric.WithDescription("Number of tool calls"),
	)

	toolDuration, _ := meter.Float64Histogram("bond.tool.duration",
		metric.WithDescription("Duration of tool calls in seconds"),
		metric.WithUnit("s"),
	)

	return &Plugin{
		tracer:           tracer,
		prefix:           opts.SpanNamePrefix,
		toolSpans:        make(map[string]trace.Span),
		streamDuration:   streamDuration,
		modelInvocations: modelInvocations,
		toolCalls:        toolCalls,
		toolDuration:     toolDuration,
		toolStarts:       make(map[string]time.Time),
	}
}

// Name implements [bond.Plugin].
func (p *Plugin) Name() string { return "otel" }

// Tools implements [bond.Plugin]. The otel plugin contributes no tools.
func (p *Plugin) Tools() []bond.Tool { return nil }

// Init implements [bond.Plugin]. Registers tracing and metric hooks.
func (p *Plugin) Init(registry *bond.HookRegistry) {
	p.initStreamHooks(registry)
	p.initModelHooks(registry)
	p.initToolHooks(registry)
}

func (p *Plugin) initStreamHooks(registry *bond.HookRegistry) {
	bond.OnBefore(registry, func(ctx context.Context, hook *bond.BeforeStreamHook) error {
		p.streamStart = time.Now()

		_, span := p.tracer.Start(ctx, p.prefix+".stream",
			trace.WithAttributes(
				attribute.Int("bond.message_count", len(hook.Messages)),
			),
		)
		p.streamSpan = span
		return nil
	})

	bond.OnAfter(registry, func(ctx context.Context, hook *bond.AfterStreamHook) {
		// Record stream duration metric.
		duration := time.Since(p.streamStart).Seconds()
		p.streamDuration.Record(ctx, duration)

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
		// Record model invocation metric with attempt attribute.
		p.modelInvocations.Add(ctx, 1,
			metric.WithAttributes(
				attribute.Int("bond.attempt", hook.Attempt),
			),
		)

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
		// Record tool start time for duration metric.
		p.mu.Lock()
		p.toolStarts[hook.ToolUse.ID] = time.Now()
		p.mu.Unlock()

		_, span := p.tracer.Start(ctx, p.prefix+".tool."+hook.ToolUse.Name,
			trace.WithAttributes(
				attribute.String("bond.tool.name", hook.ToolUse.Name),
				attribute.String("bond.tool.id", hook.ToolUse.ID),
			),
		)
		p.toolSpans[hook.ToolUse.ID] = span
		return nil
	})

	bond.OnAfter(registry, func(ctx context.Context, hook *bond.AfterToolCallHook) {
		// Compute tool duration and record metrics.
		p.mu.Lock()
		start, ok := p.toolStarts[hook.ToolUse.ID]
		if ok {
			delete(p.toolStarts, hook.ToolUse.ID)
		}
		p.mu.Unlock()

		toolAttrs := metric.WithAttributes(
			attribute.String("bond.tool.name", hook.ToolUse.Name),
			attribute.Bool("bond.tool.is_error", hook.Result.IsError),
		)

		p.toolCalls.Add(ctx, 1, toolAttrs)

		if ok {
			duration := time.Since(start).Seconds()
			p.toolDuration.Record(ctx, duration, toolAttrs)
		}

		// End trace span.
		span, spanOk := p.toolSpans[hook.ToolUse.ID]
		if !spanOk {
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

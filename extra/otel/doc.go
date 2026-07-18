// Package otel provides an OpenTelemetry observability plugin for bond agents.
//
// The plugin emits spans and metrics for the agent loop lifecycle: model
// invocations, tool calls, and the overall stream. It records token usage,
// stop reasons, tool names, and errors as span attributes and events.
//
// # Traces
//
// Spans are created for stream, model invocation, and tool call phases.
//
// # Metrics
//
// The following instruments are recorded:
//
//   - bond.stream.duration — histogram of stream durations (seconds)
//   - bond.model.invocations — counter of model invocations (attributes: attempt)
//   - bond.tool.calls — counter of tool calls (attributes: tool name, is_error)
//   - bond.tool.duration — histogram of tool call durations (seconds)
//
// # Usage
//
//	import "go.opentelemetry.io/otel"
//
//	plugin := otel.NewPlugin(otel.PluginOptions{
//	    TracerProvider: otel.GetTracerProvider(),
//	    MeterProvider:  otel.GetMeterProvider(),
//	})
//
//	resp, err := bond.Invoke(ctx, agent, messages, bond.AgentOptions{
//	    Plugins: []bond.Plugin{plugin},
//	})
//
// The plugin is compatible with any OpenTelemetry-compatible backend
// (X-Ray, Datadog, Honeycomb, Jaeger, etc.) via the standard TracerProvider
// and MeterProvider.
package otel

// Package otel provides an OpenTelemetry observability plugin for bond agents.
//
// The plugin emits spans for the agent loop lifecycle: model invocations,
// tool calls, and the overall stream. It records token usage, stop reasons,
// tool names, and errors as span attributes and events.
//
// # Usage
//
//	import "go.opentelemetry.io/otel"
//
//	plugin := otel.NewPlugin(otel.PluginOptions{
//	    TracerProvider: otel.GetTracerProvider(),
//	})
//
//	resp, err := bond.Invoke(ctx, agent, messages, bond.AgentOptions{
//	    Plugins: []bond.Plugin{plugin},
//	})
//
// The plugin is compatible with any OpenTelemetry-compatible backend
// (X-Ray, Datadog, Honeycomb, Jaeger, etc.) via the standard TracerProvider.
package otel

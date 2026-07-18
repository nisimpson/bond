package bond

import "context"

// Plugin bundles tools and hook registrations into a reusable unit.
type Plugin interface {
	// Name identifies the plugin (used for logging/debugging).
	Name() string
	// Tools returns the tools this plugin contributes to the agent loop.
	Tools() []Tool
	// Init registers hooks on the provided registry. Called once during
	// stream setup before the loop begins.
	Init(registry *HookRegistry)
}

// ContextPlugin is optionally implemented by plugins that need to inject
// values into the stream context. This is useful for request-scoped state
// like loggers, trace IDs, or session identifiers that should flow through
// the entire agent loop and be accessible from hooks and tools.
//
// InitContext is called after [Plugin.Init], so the plugin is fully set up
// (hooks registered) before it enriches the context.
type ContextPlugin interface {
	InitContext(ctx context.Context) context.Context
}

// simplePlugin is a convenience implementation of Plugin that delegates to
// stored fields and a callback function, making it easy to construct plugins
// without defining a new type for each one.
type simplePlugin struct {
	name     string
	tools    []Tool
	initFunc func(registry *HookRegistry)
}

func (s simplePlugin) Name() string                { return s.name }
func (s simplePlugin) Tools() []Tool               { return s.tools }
func (s simplePlugin) Init(registry *HookRegistry) { s.initFunc(registry) }

// NewToolsPlugin creates a [Plugin] that contributes the given tools to the agent
// loop without registering any hooks. This is useful when you only need to
// expose tools and don't require lifecycle callbacks.
func NewToolsPlugin(name string, tools ...Tool) Plugin {
	return simplePlugin{
		name:     name,
		tools:    tools,
		initFunc: func(registry *HookRegistry) { /* no op */ },
	}
}

// NewHooksPlugin creates a [Plugin] that registers hooks via the provided callback
// but does not contribute any tools. This is useful for cross-cutting concerns
// like logging, metrics, or guardrails that only need to observe or modify the
// agent loop through hooks.
func NewHooksPlugin(name string, onInit func(*HookRegistry)) Plugin {
	return simplePlugin{
		name:     name,
		initFunc: onInit,
	}
}

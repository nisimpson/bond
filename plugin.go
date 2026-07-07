package helix

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

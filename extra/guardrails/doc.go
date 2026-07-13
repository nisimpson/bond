// Package guardrails provides content filtering for bond agents.
//
// The guardrails plugin registers hooks on the agent loop to filter input
// (before model invocation) and output (after model invocation). Filters
// examine text content and return actions that determine how the content
// should be handled: allowed, blocked, redacted, warned, or logged.
//
// # Usage
//
//	plugin := guardrails.NewPlugin(guardrails.PluginOptions{
//	    Filters: []guardrails.ContentFilter{
//	        guardrails.NewTopicFilter("violence", "weapons"),
//	        guardrails.NewPatternFilter(guardrails.CommonPIIPatterns()...),
//	    },
//	    OnBlock: func(ctx context.Context, result guardrails.FilterResult) {
//	        slog.Warn("content blocked", "reason", result.Reason)
//	    },
//	})
//
//	resp, err := bond.Invoke(ctx, agent, messages, bond.AgentOptions{
//	    Plugins: []bond.Plugin{plugin},
//	})
package guardrails

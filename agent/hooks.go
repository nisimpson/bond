package agent

import "github.com/nisimpson/bond"

// ---------------------------------------------------------------------------
// Graph Hook Events
// ---------------------------------------------------------------------------

// BeforeNodeTransitionHook fires before the graph transitions to a new node.
// Return [bond.ErrAbort] to stop the graph gracefully, or any error to halt
// with that error. This enables human-in-the-loop approval gates on graph
// traversal.
type BeforeNodeTransitionHook struct {
	FromNode string // current node (empty string on initial entry)
	ToNode   string // target node being transitioned to
	State    State  // shared graph state
}

func (*BeforeNodeTransitionHook) HookEvent()       {}
func (*BeforeNodeTransitionHook) BeforeHookEvent() {}

// AfterNodeExecutionHook fires after a node has completed execution.
// This is an observer hook — it cannot interrupt the graph.
type AfterNodeExecutionHook struct {
	Node  string // the node that just executed
	State State  // shared graph state after execution
}

func (*AfterNodeExecutionHook) HookEvent()      {}
func (*AfterNodeExecutionHook) AfterHookEvent() {}

// ---------------------------------------------------------------------------
// Swarm Hook Events
// ---------------------------------------------------------------------------

// BeforeAgentHandoffHook fires before the swarm transfers control to a
// different agent. Return [bond.ErrAbort] to prevent the handoff and stop
// the swarm, or any error to halt with that error.
type BeforeAgentHandoffHook struct {
	FromAgent string // the currently active agent
	ToAgent   string // the agent being transferred to
	State     State  // shared swarm state
}

func (*BeforeAgentHandoffHook) HookEvent()       {}
func (*BeforeAgentHandoffHook) BeforeHookEvent() {}

// AfterAgentHandoffHook fires after the swarm has completed a handoff.
// This is an observer hook — it cannot interrupt the swarm.
type AfterAgentHandoffHook struct {
	FromAgent string // the previously active agent
	ToAgent   string // the now-active agent
	State     State  // shared swarm state after handoff
}

func (*AfterAgentHandoffHook) HookEvent()      {}
func (*AfterAgentHandoffHook) AfterHookEvent() {}

// Verify interface compliance.
var (
	_ bond.BeforeHookEvent = (*BeforeNodeTransitionHook)(nil)
	_ bond.AfterHookEvent  = (*AfterNodeExecutionHook)(nil)
	_ bond.BeforeHookEvent = (*BeforeAgentHandoffHook)(nil)
	_ bond.AfterHookEvent  = (*AfterAgentHandoffHook)(nil)
)

// Package approval provides human-in-the-loop approval gates for bond agents.
//
// An approval gate intercepts before-hook events and blocks execution until
// an approver (human, external service, or policy engine) grants or denies
// the operation. Gates are generic over any [bond.BeforeHookEvent], enabling
// approval workflows for tool calls, model invocations, or any other gated
// lifecycle event.
//
// # Usage
//
// Implement the [Gate] interface for a specific hook event type:
//
//	type myToolGate struct { /* transport, UI handle, etc. */ }
//
//	func (g *myToolGate) RequestApproval(ctx context.Context, event *bond.BeforeToolCallHook) (approval.Result, error) {
//	    // Present tool call to user, block until response.
//	    approved := askUser(event.ToolUse.Name, event.ToolUse.Input)
//	    return approval.Result{Approved: approved}, nil
//	}
//
// Register the gate on a hook registry (typically via a plugin's Init method):
//
//	approval.Register(registry, &myToolGate{})
//
// For simple inline logic, use [GateFunc]:
//
//	gate := approval.GateFunc[*bond.BeforeToolCallHook](func(ctx context.Context, event *bond.BeforeToolCallHook) (approval.Result, error) {
//	    if event.ToolUse.Name == "delete_all" {
//	        return approval.Result{Approved: false, Reason: "too dangerous"}, nil
//	    }
//	    return approval.Result{Approved: true}, nil
//	})
//	approval.Register(registry, gate)
//
// When a gate denies an operation, the hook returns [bond.ErrAbort], which
// causes the agent loop to skip the gated operation gracefully.
package approval

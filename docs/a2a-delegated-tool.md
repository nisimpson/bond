# A2A Delegated Tool

## Overview

A delegated tool enables agent-to-agent (A2A) communication by pausing a tool call cycle to request input from an external caller. From the model's perspective, it looks like a normal tool invocation — the delegation is transparent.

## Motivation

An agent communicating via A2A may encounter a tool call that requires information or action from the caller (another agent or a human). Rather than adding interrupt semantics to the core loop, we leverage the existing `Tool` interface: a tool's `Run` method blocks until the external party responds.

## Design

No core loop changes are needed. The `Tool.Run` method is a blocking call that returns `[]Block`. The delegated tool implementation:

1. Sends an "input required" task to the caller via A2A protocol
2. Blocks until the caller responds (or context is cancelled)
3. Returns the caller's response as tool result blocks

The agent loop sees a normal tool call that happened to take a while.

## Sequence

```
Model                Loop              DelegatedTool           Caller (A2A)
  |                    |                     |                      |
  |-- tool_use ------->|                     |                      |
  |                    |-- Run(ctx, input) ->|                      |
  |                    |                     |-- SendInputRequired ->|
  |                    |                     |                      |
  |                    |                     |   (blocks on ctx)    |
  |                    |                     |                      |
  |                    |                     |<-- Response ---------|
  |                    |<-- []Block ---------|                      |
  |                    |                     |                      |
  |<-- tool_result ----|                     |                      |
  |                    |                     |                      |
  | (continues)        |                     |                      |
```

## Implementation Notes

### Tool struct

```go
type DelegatedTool struct {
    name        string
    description string
    schema      map[string]any
    a2aClient   A2AClient
}

func (d *DelegatedTool) Name() string              { return d.name }
func (d *DelegatedTool) Description() string       { return d.description }
func (d *DelegatedTool) InputSchema() map[string]any { return d.schema }

func (d *DelegatedTool) Run(ctx context.Context, input map[string]any) ([]Block, error) {
    // 1. Send "input required" task to the caller via A2A
    taskID, err := d.a2aClient.SendInputRequired(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("delegated tool: send input required: %w", err)
    }

    // 2. Block until the caller responds (or ctx is cancelled)
    response, err := d.a2aClient.AwaitResponse(ctx, taskID)
    if err != nil {
        return nil, fmt.Errorf("delegated tool: await response: %w", err)
    }

    // 3. Return caller's response as tool result
    return response.ToBlocks(), nil
}
```

### A2AClient interface (to define)

```go
type A2AClient interface {
    // SendInputRequired sends a task to the caller indicating input is needed.
    // Returns a task ID for tracking the response.
    SendInputRequired(ctx context.Context, input map[string]any) (string, error)

    // AwaitResponse blocks until the caller responds to the given task.
    // Returns the response payload. Must respect context cancellation.
    AwaitResponse(ctx context.Context, taskID string) (*A2AResponse, error)
}

type A2AResponse struct {
    // Content holds the response data from the caller.
    Content []Block
}

func (r *A2AResponse) ToBlocks() []Block {
    return r.Content
}
```

### Plugin packaging

The delegated tool can be packaged as a plugin:

```go
type A2APlugin struct {
    client A2AClient
    tools  []*DelegatedTool
}

func (p *A2APlugin) Name() string    { return "a2a" }
func (p *A2APlugin) Tools() []Tool   { /* return p.tools */ }
func (p *A2APlugin) Init(r *HookRegistry) {
    // Optional: register hooks for observability
    // e.g., log when delegation starts/completes
}
```

## Concurrency

Since the agent loop executes tools concurrently, a blocking delegated tool does not prevent other tools in the same cycle from executing. If the overall stream context is cancelled, the delegated tool's `AwaitResponse` should detect `ctx.Done()` and return an error.

## Error Handling

- **Caller never responds**: Context timeout/cancellation causes `AwaitResponse` to return an error. The loop wraps it as an error `ToolResultBlock`, and the model can decide how to proceed.
- **A2A transport failure**: `SendInputRequired` returns an error, which becomes an error tool result.
- **Caller returns an error**: Encode as `IsError: true` on the result block so the model knows the delegation failed.

## Hook Integration

Optional hooks for observability:

- `BeforeToolCallHook`: Can log/audit that a delegation is about to happen
- `AfterToolCallHook`: Can log/audit the delegated result, measure latency

No special hook types are needed — the existing lifecycle hooks cover this pattern.

## Open Questions

- [ ] Define the A2A protocol/transport (HTTP, gRPC, channels for in-process?)
- [ ] Task ID generation strategy
- [ ] Timeout policy: per-tool configurable timeout vs. relying solely on ctx?
- [ ] Should `AwaitResponse` support streaming partial updates back?
- [ ] Authentication/authorization between agents

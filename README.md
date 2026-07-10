# Bond 

<p align="center">
  <img src=".github/assets/banner.png" alt="Bond" width="600">
</p>

[![Test](https://github.com/nisimpson/bond/actions/workflows/test.yml/badge.svg)](https://github.com/nisimpson/bond/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/nisimpson/bond.svg)](https://pkg.go.dev/github.com/nisimpson/bond)
[![Release](https://img.shields.io/github/release/nisimpson/bond.svg)](https://github.com/nisimpson/bond/releases)

The name's Bond. *Agent* Bond.

A(nother) Go framework for building agentic applications. Bond provides the streaming loop, tool execution, orchestration primitives, and runtime integrations — you bring the model. License to build.

## Features

- **Streaming agent loop** with parallel tool execution and context cancellation
- **Provider-agnostic** — implement `bond.Agent` for any LLM (Bedrock included)
- **Hooks and plugins** for cross-cutting concerns (logging, guardrails, metrics)
- **Orchestration patterns** — graphs (LangGraph-style) and swarms (OpenAI Swarm-style)
- **[A2A](https://google.github.io/A2A/) protocol support** — remote agent communication, tool delegation
- **AgentCore runtime** — deploy to AWS Bedrock AgentCore with A2A, HTTP, and MCP handlers
- **MCP tool adapter** — use [MCP](https://modelcontextprotocol.io/) server tools in your agent
- **Tool registry** — expose large tool collections through a discovery gateway
- **Zero external deps in the root** — sub-packages isolate SDK dependencies

## Install

```bash
go get github.com/nisimpson/bond
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/nisimpson/bond"
    "github.com/nisimpson/bond/provider/bedrock"
    "github.com/aws/aws-sdk-go-v2/config"
    bedrockrt "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func main() {
    ctx := context.Background()
    cfg, _ := config.LoadDefaultConfig(ctx)
    client := bedrockrt.NewFromConfig(cfg)

    // Every agent needs a mission briefing.
    agent := bedrock.New(client, bedrock.AgentOptions{
        ModelID: "anthropic.claude-sonnet-4-20250514-v1:0",
        System:  "You are a secret agent. Be helpful, be discreet.",
    })

    resp, err := bond.Invoke(ctx, agent, bond.TextPrompt("Brief me on the situation."), bond.AgentOptions{})
    if err != nil {
        panic(err)
    }
    fmt.Println(resp.Text)
}
```

## Core Concepts

### Agent

The `bond.Agent` interface is the single abstraction at the center of everything:

```go
type Agent interface {
    Stream(ctx context.Context, messages []Message) iter.Seq2[StreamEvent, error]
}
```

Anything that implements `Agent` can participate in bond: model providers, A2A proxies, graphs, swarms, test doubles.

### Stream and Invoke

```go
// Streaming — live intel as it arrives
for event, err := range bond.Stream(ctx, agent, messages, opts) { ... }

// Synchronous — debrief in one shot
resp, err := bond.Invoke(ctx, agent, messages, opts)
```

### Tools (Gadgets)

Every good agent needs gadgets. Define them with `NewFuncTool`:

```go
laserWatch, _ := bond.NewFuncTool(
    func(ctx context.Context, in CutInput) (CutOutput, error) {
        return CutOutput{Result: "lock disabled"}, nil
    },
    bond.FuncToolOptions{
        Name:        "laser_watch",
        Description: "Cuts through locks and barriers",
        InputSchema: schema.For[CutInput](),
    },
)
```

Equip your agent:

```go
bond.Stream(ctx, agent, msgs, bond.AgentOptions{
    Tools:    []bond.Tool{laserWatch, grappleHook, smokeBomb},
    MaxTurns: 10,
})
```

### Plugins (Q Branch)

Plugins bundle gadgets and lifecycle hooks — your personal Q Branch:

```go
bond.Stream(ctx, agent, msgs, bond.AgentOptions{
    Plugins: []bond.Plugin{surveillancePlugin, counterIntelPlugin},
})
```

## Package Structure

```
bond/                        Core interfaces (Agent, Tool, Block, Stream, Invoke)
bond/agent/                  Orchestration: graph, swarm, A2A adapter, AsTool
bond/provider/bedrock/       Amazon Bedrock Converse streaming provider
bond/runtime/agentcore/      AWS AgentCore handlers (A2A, HTTP, MCP)
bond/tool/schema/            JSON Schema generation and structured output validation
bond/tool/toolmcp/           MCP server tool adapter
bond/extra/delegation/       A2A tool delegation (client + server)
bond/extra/toolregistry/     Tool discovery gateway plugin
bond/bondtest/               Test utilities (deterministic agent, event helpers)
```

## Orchestration

### Graph (Mission Planning)

Route between agents with conditional edges and shared state:

```go
g := agent.NewGraph("intake", agent.GraphOptions{})

g.AddNode("intake", &agent.GraphNode{Agent: dispatchAgent})
g.AddNode("fieldwork", &agent.GraphNode{Agent: fieldAgent})
g.AddNode("gather_intel", &agent.GraphNode{
    Action: func(ctx context.Context, state agent.State) error {
        state.Set("dossier", fetchDossier(ctx))
        return nil
    },
})

g.AddConditionalEdge("intake", func(state agent.State) string {
    threat, _ := state.Get("threat_level")
    if threat == "critical" { return "gather_intel" }
    return agent.EndNode
})
g.AddEdge("gather_intel", "fieldwork")

bond.Invoke(ctx, g, bond.TextPrompt("new assignment"), bond.AgentOptions{})
```

### Swarm (Multi-Agent Cell)

Agents transfer control dynamically — a cell of operatives:

```go
s := agent.NewSwarm("handler", agent.SwarmOptions{})

s.AddAgent("handler", &agent.SwarmAgent{
    Agent:       handlerAgent,
    Description: "Coordinates operations and dispatches field agents.",
})
s.AddAgent("infiltrator", &agent.SwarmAgent{
    Agent:       infiltratorAgent,
    Description: "Specializes in social engineering and access.",
})
s.AddAgent("analyst", &agent.SwarmAgent{
    Agent:       analystAgent,
    Description: "Processes intelligence and provides situational analysis.",
})

// The handler decides when to bring in specialists
bond.Invoke(ctx, s, bond.TextPrompt("we need eyes inside"), bond.AgentOptions{})
```

### Sub-Agent Tool (Delegation)

Wrap any agent as a tool — delegate missions to specialists:

```go
tool := agent.AsTool(researchAgent, agent.ToolOptions{
    Name:        "research_operative",
    Description: "Delegates intelligence gathering to a specialist",
    StreamOptions: bond.AgentOptions{Tools: osintTools},
})
```

## Tool Delegation (Double Agent Protocol)

When agents communicate over A2A, a client agent can lend its tools to a server agent. The server uses them as if they were local — never knowing the gadgets belong to someone else.

### Client Side (The Quartermaster)

```go
// Your agent connects to a remote specialist and offers its gadgets.
specialist := delegation.NewAgent(delegation.AgentOptions{
    Client: a2aClient,  // connected to remote agent
    Tools:  []bond.Tool{searchTool, databaseTool, satelliteTool},
})

// Use it like any agent — delegation is transparent.
resp, _ := bond.Invoke(ctx, specialist, bond.TextPrompt("find the target"), bond.AgentOptions{})
```

### Server Side (The Field Agent)

```go
// The server negotiates: "tell me what gadgets you have."
// Then it uses them as its own.
executor := delegation.NewExecutor(delegation.ExecutorOptions{
    Factory: func(ctx context.Context, skills []delegation.Skill, requester delegation.Requester) (bond.Agent, bond.AgentOptions) {
        agent := bedrock.New(client, bedrock.AgentOptions{
            ModelID: "anthropic.claude-sonnet-4-20250514-v1:0",
            System:  "You are a field operative. Use your available tools.",
        })
        return agent, bond.AgentOptions{
            Plugins: []bond.Plugin{
                delegation.NewPlugin(delegation.Options{
                    Requester: requester,
                    Skills:    skills,
                }),
            },
        }
    },
})

handler := agentcore.NewA2AHandlerFromExecutor(executor, agentcore.Options{})
http.ListenAndServe(handler.Port(), handler)
```

The server's model calls a delegated tool → Bond sends "input required" back to the client → the client executes the tool locally → result returns seamlessly. The model never knows the gadget was remote. *Shaken, not stirred.*

## Runtime: AgentCore (Field Deployment)

Deploy your agent to AWS Bedrock AgentCore:

```go
// A2A protocol (port 9000) — agent-to-agent communication
a2aHandler := agentcore.NewA2AHandler(agent, agentcore.Options{
    Card: &a2a.AgentCard{Name: "007", Description: "Licensed to assist"},
    AgentOptions: bond.AgentOptions{Tools: myTools},
})
http.ListenAndServe(a2aHandler.Port(), a2aHandler)

// HTTP protocol (port 8080) — direct invocations
httpHandler := agentcore.NewHTTPHandler(agent, opts)
http.ListenAndServe(httpHandler.Port(), httpHandler)

// MCP protocol (port 8000) — A2A operations as MCP tools
mcpHandler := agentcore.NewMCPHandler(agent, opts)
http.ListenAndServe(mcpHandler.Port(), mcpHandler)

// Or deploy all at once with graceful shutdown
agentcore.Serve(agent, opts)
```

## MCP Integration

Equip your agent with tools from any MCP server:

```go
session, _ := client.Connect(ctx, transport, nil)
tools, _ := toolmcp.ServerTools(ctx, session)

bond.Stream(ctx, agent, msgs, bond.AgentOptions{Tools: tools})
```

## Tool Registry (The Armory)

When you have more gadgets than an agent can carry, use the registry:

```go
registry := toolregistry.New(toolregistry.Options{
    Tools: fiftyGadgets,
})

// Agent sees 3 tools: list_tools, describe_tool, use_tool
// It discovers what it needs on demand.
bond.Stream(ctx, agent, msgs, bond.AgentOptions{
    Plugins: []bond.Plugin{registry},
})
```

## Testing (Training Exercise)

```go
double := &bondtest.Agent{Events: bondtest.TextEvents("Mission accomplished.")}
resp, _ := bond.Invoke(ctx, double, bond.TextPrompt("status report"), bond.AgentOptions{})
// resp.Text == "Mission accomplished."
```

## License

[MIT](LICENSE)

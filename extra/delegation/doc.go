// Package delegation enables transparent tool delegation between agents
// communicating over the A2A protocol.
//
// # Problem
//
// When agents communicate over A2A, a server agent may need capabilities
// that only the client possesses (database access, search, proprietary APIs).
// Rather than requiring the server to have direct access to these tools, the
// client can "lend" its tools to the server. The server uses them as if they
// were local, and the client executes them on the server's behalf.
//
// # Architecture
//
// The delegation package has two sides:
//
//   - Server side: receives skills from the client, creates proxy tools that
//     send "input required" requests back to the client when invoked.
//   - Client side: advertises skills, handles incoming "input required" requests
//     by executing tools locally, and sends results back.
//
// # Server Side Usage
//
// The server agent uses the [Plugin] to claim client skills as proxy tools:
//
//	// Skills received from client (via agent card or message metadata)
//	skills := []delegation.Skill{
//	    {Name: "search", Description: "Search the web", InputSchema: searchSchema},
//	    {Name: "db_query", Description: "Query the database", InputSchema: dbSchema},
//	}
//
//	// Requester handles the A2A transport for "input required" tasks
//	requester := &myRequester{clientClient: a2aClient}
//
//	// Create plugin — proxy tools are added to the agent loop
//	plugin := delegation.NewPlugin(delegation.Options{
//	    Requester: requester,
//	    Skills:    skills,
//	})
//
//	// Server agent sees "search" and "db_query" as normal tools
//	bond.Stream(ctx, serverAgent, messages, bond.AgentOptions{
//	    Plugins: []bond.Plugin{plugin},
//	})
//
// When the server's model calls "search", the proxy tool sends an "input
// required" task to the client (via Requester.RequestInput), blocks until
// the client responds, and returns the response as a normal tool result.
// The model never knows the tool was remote.
//
// # Client Side Usage
//
// The client uses skillsFromTools to advertise capabilities and fulfiller
// to execute delegated requests:
//
//	// Extract skills from local tools for advertisement
//	skills := delegation.skillsFromTools([]bond.Tool{searchTool, dbTool})
//	// → send skills in agent card or message metadata to server
//
//	// Create fulfiller for handling incoming requests
//	fulfiller := delegation.newFulfiller(searchTool, dbTool)
//
// # Transparent Invocation with [Invoke]
//
// For the common case where the client sends a task to the server and wants
// to handle all delegation round-trips automatically:
//
//	result, err := delegation.Invoke(ctx, delegation.InvokeOptions{
//	    Client:    a2aClient,        // connected to server agent
//	    Fulfiller: fulfiller,        // handles "input required" locally
//	    Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("write an article about Go")),
//	})
//	// result.Text contains the server's final output
//	// All intermediate tool delegations were handled transparently
//
// [Invoke] streams events from the server. When it sees a TaskStatusUpdateEvent
// with state "input-required", it parses the tool request, calls
// fulfiller.Execute locally, sends the result back to the server, and continues
// watching the stream. The client's own agent loop and conversation history
// are never affected.
//
// # Sequence Diagram
//
//	Client                          Server Agent
//	  │                                │
//	  │── SendMessage("write article")─>│
//	  │                                │ model runs...
//	  │                                │ calls "search" tool
//	  │                                │
//	  │<─ TaskStatus: input-required ──│ (proxy tool blocks)
//	  │   {"tool":"search",            │
//	  │    "input":{"q":"Go history"}} │
//	  │                                │
//	  │ fulfiller.Execute("search",    │
//	  │   {"q":"Go history"})          │
//	  │ → runs search locally          │
//	  │                                │
//	  │── SendMessage(search results) ─>│ (proxy tool unblocks)
//	  │                                │
//	  │                                │ model continues...
//	  │                                │ produces final article
//	  │                                │
//	  │<─ TaskArtifact: article text ──│
//	  │<─ TaskStatus: completed ───────│
//	  │                                │
//	  │ result.Text = article          │
//
// # Input Required Message Format
//
// The "input required" status message contains a JSON payload describing
// which tool to call:
//
//	{"tool": "search", "input": {"query": "Go programming language history"}}
//
// This is sent as a TextPart in the TaskStatusUpdateEvent's Status.Message.
// The server's Requester implementation is responsible for constructing this
// message when sending "input required" back to the client.
//
// # Key Design Properties
//
//   - Client context is never polluted: tool results only flow to the server
//   - The server model is unaware of delegation: proxy tools look identical to local tools
//   - Concurrent tool calls work: if the server requests multiple delegated tools,
//     they execute concurrently on the client (via bond's parallel tool execution)
//   - Context cancellation propagates: if the client's context is cancelled,
//     the proxy tool on the server side returns an error
//   - No special infrastructure: the client doesn't need to run a server;
//     delegation happens inline on the existing A2A streaming connection
//
// # Server Side with Executor
//
// For agents deployed on AgentCore (or any A2A server), use [Executor]
// to handle skills negotiation automatically. It implements a2asrv.AgentExecutor,
// gates execution until the client provides skills, and passes them to your
// factory so you can wire the delegation plugin yourself:
//
//	executor := delegation.NewExecutor(delegation.ExecutorOptions{
//	    Factory: func(ctx context.Context, skills []delegation.Skill, requester delegation.Requester) (bond.Agent, bond.AgentOptions) {
//	        agent := bedrock.New(client, bedrock.AgentOptions{
//	            ModelID: "anthropic.claude-sonnet-4-20250514-v1:0",
//	            System:  "You are a writer.",
//	        })
//	        return agent, bond.AgentOptions{
//	            MaxTurns: 10,
//	            Plugins: []bond.Plugin{
//	                delegation.NewPlugin(delegation.Options{
//	                    Requester: myRequester,
//	                    Skills:    skills,
//	                }),
//	            },
//	        }
//	    },
//	})
//
//	handler := agentcore.NewA2AHandlerFromExecutor(executor, agentcore.Options{...})
//	http.ListenAndServe(handler.Port(), handler)
//
// The executor handles the negotiation protocol:
//
//  1. First message arrives without skills → responds "input required: send skills"
//  2. Client responds with skills in metadata → factory is called with skills
//  3. Subsequent messages reuse the cached agent (keyed by ContextID)
//
// The factory receives skills and is responsible for creating the delegation
// plugin. This avoids magic injection and gives you full control over how
// skills are wired into the agent.
package delegation

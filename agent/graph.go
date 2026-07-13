package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/nisimpson/bond"
)

// EndNode is the sentinel name indicating the graph should stop execution.
const EndNode = "__end__"

// EdgeCondition evaluates the shared state and returns the name of the next
// node to transition to. Return [EndNode] to terminate.
type EdgeCondition func(state State) string

// ActionFunc is a non-agentic node that performs an action (typically state mutation).
type ActionFunc func(ctx context.Context, state State) error

// GraphNode represents a node in the agent graph. A node is either an agent
// node (Agent is set) or an action node (Action is set), not both.
type GraphNode struct {
	// Agent node — runs bond.Stream with tools and hooks.
	Agent   bond.Agent
	Options bond.AgentOptions

	// Action node — runs a function that can read/write state.
	// Mutually exclusive with Agent.
	Action ActionFunc
}

// edge represents a transition between nodes.
type edge struct {
	to        string        // static target (empty if conditional)
	condition EdgeCondition // dynamic target (nil if static)
}

// GraphOptions configures a Graph.
type GraphOptions struct {
	// State is the shared state for the graph. If nil, a MapState is created.
	State State
}

// Graph is an agent that orchestrates execution across a directed graph of
// sub-agents and action nodes. Each agent node runs its own [bond.Stream]
// loop internally; the graph yields events transparently to the caller.
//
// Graph implements [bond.Agent].
//
// Example:
//
//	g := agent.NewGraph("triage", agent.GraphOptions{
//	    State: agent.MapState{"user_tier": "premium"},
//	})
//
//	// Agent node — runs a full LLM loop with auto-injected state tools.
//	g.AddNode("triage", &agent.GraphNode{Agent: triageAgent, Options: triageOpts})
//	g.AddNode("billing_agent", &agent.GraphNode{Agent: billingAgent, Options: billingOpts})
//
//	// Action node — pure function, no LLM. Reads/writes state directly.
//	g.AddNode("fetch_order", &agent.GraphNode{
//	    Action: func(ctx context.Context, state agent.State) error {
//	        orderID, _ := state.Get("order_id")
//	        order := db.FetchOrder(ctx, orderID.(string))
//	        state.Set("order", order)
//	        return nil
//	    },
//	})
//
//	// Static edge: fetch_order always goes to billing_agent.
//	g.AddEdge("fetch_order", "billing_agent")
//
//	// Conditional edge: triage routes based on state.
//	g.AddConditionalEdge("triage", func(state agent.State) string {
//	    cat, _ := state.Get("category")
//	    switch cat {
//	    case "billing":
//	        return "fetch_order"
//	    default:
//	        return agent.EndNode
//	    }
//	})
//
//	// Use like any other agent.
//	for event, err := range bond.Stream(ctx, g, bond.TextPrompt("help me"), bond.StreamOptions{}) {
//	    // events flow transparently from all nodes
//	}
type Graph struct {
	nodes map[string]*GraphNode
	edges map[string][]edge
	entry string
	state State
}

// NewGraph creates a graph with the given entry node name and options.
func NewGraph(entry string, opts GraphOptions) *Graph {
	state := opts.State
	if state == nil {
		state = NewMapState()
	}
	return &Graph{
		nodes: make(map[string]*GraphNode),
		edges: make(map[string][]edge),
		entry: entry,
		state: state,
	}
}

// AddNode registers a named node in the graph.
func (g *Graph) AddNode(name string, node *GraphNode) {
	g.nodes[name] = node
}

// AddEdge adds a static (unconditional) transition from one node to another.
func (g *Graph) AddEdge(from, to string) {
	g.edges[from] = append(g.edges[from], edge{to: to})
}

// AddConditionalEdge adds a dynamic transition that evaluates at runtime
// based on the shared state.
func (g *Graph) AddConditionalEdge(from string, condition EdgeCondition) {
	g.edges[from] = append(g.edges[from], edge{condition: condition})
}

// Stream implements [bond.Agent]. It walks the graph starting from the entry
// node, running each node's agent or action and forwarding events to the caller.
func (g *Graph) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		// Attach state to context so tools can access it.
		ctx = ContextWithState(ctx, g.state)
		hooks := bond.HookRegistryFromContext(ctx)
		history := append([]bond.Message{}, messages...)
		current := g.entry
		prev := ""

		for current != EndNode {
			if ctx.Err() != nil {
				yield(bond.StreamEvent{}, ctx.Err())
				return
			}

			// BeforeNodeTransition — approval gate for graph traversal.
			if err := hooks.Notify(ctx, &BeforeNodeTransitionHook{
				FromNode: prev,
				ToNode:   current,
				State:    g.state,
			}); err != nil {
				if errors.Is(err, bond.ErrAbort) {
					yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
					return
				}
				yield(bond.StreamEvent{}, fmt.Errorf("graph: before node transition: %w", err))
				return
			}

			node, exists := g.nodes[current]
			if !exists {
				yield(bond.StreamEvent{}, fmt.Errorf("graph: unknown node %q", current))
				return
			}

			if node.Agent != nil {
				newHistory, ok := g.runAgentNode(ctx, node, history, yield)
				if !ok {
					return
				}
				history = newHistory
			} else if node.Action != nil {
				if err := node.Action(ctx, g.state); err != nil {
					yield(bond.StreamEvent{}, fmt.Errorf("graph: action node %q: %w", current, err))
					return
				}
			}

			// AfterNodeExecution — observer hook.
			_ = hooks.Notify(ctx, &AfterNodeExecutionHook{Node: current, State: g.state})

			// Evaluate edges to determine next node.
			prev = current
			current = g.nextNode(current)
		}

		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// runAgentNode executes an agent node, forwarding events and appending
// the accumulated text to history. Returns false if yield was cancelled.
func (g *Graph) runAgentNode(ctx context.Context, node *GraphNode, history []bond.Message, yield func(bond.StreamEvent, error) bool) ([]bond.Message, bool) {
	// Prepend state tools to the node's options.
	opts := node.Options
	opts.Tools = append(stateTools(g.state), opts.Tools...)

	var textBuf strings.Builder
	for event, err := range bond.Stream(ctx, node.Agent, history, opts) {
		if err != nil {
			yield(bond.StreamEvent{}, err)
			return nil, false
		}

		if !yield(event, nil) {
			return nil, false
		}

		if event.Type == bond.StreamEventTextDelta {
			textBuf.WriteString(event.TextDelta)
		}
	}

	// Append assistant output to shared history.
	if textBuf.Len() > 0 {
		history = append(history, bond.Message{
			Role:    bond.RoleAssistant,
			Content: []bond.Block{&bond.TextBlock{Text: textBuf.String()}},
		})
	}

	return history, true
}

// nextNode evaluates outgoing edges from the current node and returns the
// next node name. Returns EndNode if there are no outgoing edges.
func (g *Graph) nextNode(current string) string {
	edges, exists := g.edges[current]
	if !exists || len(edges) == 0 {
		return EndNode
	}

	for _, e := range edges {
		if e.condition != nil {
			if next := e.condition(g.state); next != "" {
				return next
			}
		} else {
			return e.to
		}
	}

	return EndNode
}

// Verify interface compliance.
var _ bond.Agent = (*Graph)(nil)

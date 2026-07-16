package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strings"
	"sync"

	"github.com/nisimpson/bond"
	"golang.org/x/sync/errgroup"
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

// parallelEdge represents a fan-out to multiple nodes with a join point.
// When the source node completes, all target nodes execute concurrently.
// After all targets finish, execution proceeds to the join node.
type parallelEdge struct {
	targets []string // nodes to execute concurrently
	join    string   // node to proceed to after all targets complete
}

// GraphOptions configures a Graph.
type GraphOptions struct {
	// State is the shared state for the graph. If nil, a MapState is created.
	State State
	// CheckpointStore persists graph execution snapshots after each node.
	// If nil, checkpointing is disabled.
	CheckpointStore CheckpointStore
	// CheckpointID is the snapshot ID key used when saving/loading/deleting
	// checkpoints. If empty and CheckpointStore is set, a default ID of
	// "graph-<entry>" is generated to avoid key collisions when multiple
	// graphs share a store.
	CheckpointID string
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
	nodes         map[string]*GraphNode
	edges         map[string][]edge
	parallelEdges map[string]*parallelEdge
	entry         string
	state         State

	// checkpointStore persists execution snapshots. Nil disables checkpointing.
	checkpointStore CheckpointStore
	// checkpointID is the snapshot key for checkpoint operations.
	checkpointID string
}

// NewGraph creates a graph with the given entry node name and options.
func NewGraph(entry string, opts GraphOptions) *Graph {
	state := opts.State
	if state == nil {
		state = NewMapState()
	}
	checkpointID := opts.CheckpointID
	if opts.CheckpointStore != nil && checkpointID == "" {
		checkpointID = "graph-" + entry
	}
	return &Graph{
		nodes:           make(map[string]*GraphNode),
		edges:           make(map[string][]edge),
		parallelEdges:   make(map[string]*parallelEdge),
		entry:           entry,
		state:           state,
		checkpointStore: opts.CheckpointStore,
		checkpointID:    checkpointID,
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

// FanOutEdge adds a fan-out edge from a source node to multiple target
// nodes that execute concurrently. After all targets complete, execution
// proceeds to the join node.
//
// During parallel execution, only text output ([bond.StreamEventTextDelta])
// from agent nodes is collected and merged into history. Other event types
// (tool-use, structured output) produced by parallel branches are not
// yielded to the caller. If full event visibility is required, use
// sequential edges instead.
func (g *Graph) FanOutEdge(from string, targets []string, join string) {
	g.parallelEdges[from] = &parallelEdge{
		targets: targets,
		join:    join,
	}
}

// Stream implements [bond.Agent]. It walks the graph starting from the entry
// node, running each node's agent or action and forwarding events to the caller.
func (g *Graph) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		ctx = ContextWithState(ctx, g.state)
		history := append([]bond.Message{}, messages...)

		g.runGraph(ctx, g.entry, "", history, yield)
	}
}

// runGraph is the shared execution loop used by both [Graph.Stream] and
// [Graph.ResumeFrom]. It walks the graph from current (with prev as the
// preceding node) until it reaches [EndNode] or an error occurs.
func (g *Graph) runGraph(ctx context.Context, current string, prev string, history []bond.Message, yield func(bond.StreamEvent, error) bool) {
	hooks := bond.HookRegistryFromContext(ctx)

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

		// Check for parallel edges first.
		if pe, ok := g.parallelEdges[current]; ok {
			newHistory, ok := g.runParallelNodes(ctx, pe, current, history, hooks, yield)
			if !ok {
				return
			}
			history = newHistory
			current = pe.join
		} else {
			current = g.nextNode(prev)
		}

		// Persist checkpoint after node execution and edge resolution.
		if g.checkpointStore != nil {
			if current == EndNode {
				// Normal completion — clean up checkpoint.
				_ = g.checkpointStore.Delete(ctx, g.checkpointID)
			} else {
				snapshot := &Snapshot{
					ID:       g.checkpointID,
					Node:     prev,
					NextNode: current,
					State:    serializeState(g.state),
					History:  history,
				}
				if err := g.checkpointStore.Save(ctx, g.checkpointID, snapshot); err != nil {
					yield(bond.StreamEvent{}, fmt.Errorf("graph: checkpoint save: %w", err))
					return
				}
			}
		}
	}

	yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
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

// serializeState converts a [State] into a map[string]any using Keys and Get.
func serializeState(s State) map[string]any {
	keys := s.Keys()
	m := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := s.Get(k); ok {
			m[k] = v
		}
	}
	return m
}

// ResumeFrom loads a snapshot and resumes graph execution from the
// checkpointed next node. It restores state and history before continuing.
// Returns an error-yielding iterator if the checkpoint store is not
// configured or the snapshot cannot be loaded.
//
// ResumeFrom performs additive state restoration — it sets keys from the
// snapshot but does not clear pre-existing keys. Callers should use a fresh
// Graph instance (or clear state manually) to avoid stale key leakage from
// prior executions.
func (g *Graph) ResumeFrom(ctx context.Context, snapshotID string) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		if g.checkpointStore == nil {
			yield(bond.StreamEvent{}, fmt.Errorf("graph: checkpoint store not configured"))
			return
		}

		snapshot, err := g.checkpointStore.Load(ctx, snapshotID)
		if err != nil {
			if errors.Is(err, ErrSnapshotNotFound) {
				yield(bond.StreamEvent{}, fmt.Errorf("graph: resume: snapshot %q not found: %w", snapshotID, err))
				return
			}
			yield(bond.StreamEvent{}, fmt.Errorf("graph: resume: load snapshot: %w", err))
			return
		}

		// Restore state from the snapshot.
		for k, v := range snapshot.State {
			g.state.Set(k, v)
		}

		// Restore history and entry point from the snapshot.
		history := append([]bond.Message{}, snapshot.History...)

		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		ctx = ContextWithState(ctx, g.state)

		g.runGraph(ctx, snapshot.NextNode, snapshot.Node, history, yield)
	}
}

// Verify interface compliance.
var _ bond.Agent = (*Graph)(nil)

// parallelNodeResult holds the output of a single parallel node execution.
type parallelNodeResult struct {
	name string
	text string
}

// runParallelNodes executes all target nodes in a parallel edge concurrently.
// It fires BeforeNodeTransitionHook for each target before launch, runs all
// nodes via errgroup, fires AfterNodeExecutionHook for each after completion,
// and merges outputs into history in sorted-by-name order.
// Returns the updated history and true if successful, or false if yield was
// cancelled or an error occurred.
func (g *Graph) runParallelNodes(
	ctx context.Context,
	pe *parallelEdge,
	source string,
	history []bond.Message,
	hooks *bond.HookRegistry,
	yield func(bond.StreamEvent, error) bool,
) ([]bond.Message, bool) {
	// Sort targets by name for deterministic ordering.
	targets := make([]string, len(pe.targets))
	copy(targets, pe.targets)
	sort.Strings(targets)

	// Fire BeforeNodeTransitionHook for each target before launching.
	for _, target := range targets {
		if err := hooks.Notify(ctx, &BeforeNodeTransitionHook{
			FromNode: source,
			ToNode:   target,
			State:    g.state,
		}); err != nil {
			if errors.Is(err, bond.ErrAbort) {
				yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
				return nil, false
			}
			yield(bond.StreamEvent{}, fmt.Errorf("graph: before node transition (parallel %q): %w", target, err))
			return nil, false
		}
	}

	// Launch goroutines via errgroup with shared context for cancellation.
	eg, egCtx := errgroup.WithContext(ctx)

	// mu protects results slice from concurrent writes.
	var mu sync.Mutex
	results := make([]parallelNodeResult, 0, len(targets))

	for _, target := range targets {
		target := target // capture loop variable
		node, exists := g.nodes[target]
		if !exists {
			yield(bond.StreamEvent{}, fmt.Errorf("graph: unknown parallel node %q", target))
			return nil, false
		}

		eg.Go(func() error {
			if node.Agent != nil {
				// Run agent node, collecting text output into a buffer.
				// NOTE: Only StreamEventTextDelta is collected here. Other event
				// types (tool-use, structured output) from parallel branches are
				// silently discarded and not yielded to the caller.
				opts := node.Options
				opts.Tools = append(stateTools(g.state), opts.Tools...)

				var textBuf strings.Builder
				for event, err := range bond.Stream(egCtx, node.Agent, history, opts) {
					if err != nil {
						return fmt.Errorf("graph: parallel agent node %q: %w", target, err)
					}
					if event.Type == bond.StreamEventTextDelta {
						textBuf.WriteString(event.TextDelta)
					}
				}

				mu.Lock()
				results = append(results, parallelNodeResult{name: target, text: textBuf.String()})
				mu.Unlock()
			} else if node.Action != nil {
				if err := node.Action(egCtx, g.state); err != nil {
					return fmt.Errorf("graph: parallel action node %q: %w", target, err)
				}
				mu.Lock()
				results = append(results, parallelNodeResult{name: target, text: ""})
				mu.Unlock()
			}
			return nil
		})
	}

	// Wait for all goroutines to complete.
	if err := eg.Wait(); err != nil {
		yield(bond.StreamEvent{}, err)
		return nil, false
	}

	// Fire AfterNodeExecutionHook for each target (sorted order).
	for _, target := range targets {
		_ = hooks.Notify(ctx, &AfterNodeExecutionHook{Node: target, State: g.state})
	}

	// Sort results by name for deterministic history ordering.
	sort.Slice(results, func(i, j int) bool {
		return results[i].name < results[j].name
	})

	// Merge outputs into history in sorted order.
	for _, r := range results {
		if r.text != "" {
			history = append(history, bond.Message{
				Role:    bond.RoleAssistant,
				Content: []bond.Block{&bond.TextBlock{Text: r.text}},
			})
		}
	}

	return history, true
}

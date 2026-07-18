package agent_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent"
	"github.com/nisimpson/bond/bondtest"
)

func ExampleNewGraph() {
	// A simple graph: classify (action) → conditional → respond (agent)
	respond := &bondtest.Agent{
		Events: bondtest.TextEvents("I can help with your bill."),
	}

	g := agent.NewGraph("classify", agent.GraphOptions{})

	g.AddNode("classify", &agent.GraphNode{
		Action: func(ctx context.Context, state agent.State) error {
			state.Set("category", "billing")
			return nil
		},
	})
	g.AddNode("respond", &agent.GraphNode{Agent: respond})

	g.AddConditionalEdge("classify", func(state agent.State) string {
		cat, _ := state.Get("category")
		if cat == "billing" {
			return "respond"
		}
		return agent.EndNode
	})

	resp, err := bond.Invoke(context.Background(), g, bond.TextPrompt("my bill is wrong"), bond.AgentOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text)
	// Output: I can help with your bill.
}

func ExampleNewSwarm() {
	// Handler transfers to specialist, specialist responds.
	// Sequence provides two invocations: the first emits a transfer tool call,
	// the second completes the bond.Stream tool loop with no additional text.
	handler := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID:    "xfer_1",
				Name:  "transfer_to_specialist",
				Input: json.RawMessage(`{}`),
			}),
			// After bond.Stream processes the tool result, re-invoke yields end.
			[]bond.StreamEvent{
				{Type: bond.StreamEventStart},
				{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd},
			},
		),
	}
	specialist := &bondtest.Agent{
		Events: bondtest.TextEvents("I'm the specialist. Here's your answer."),
	}

	s := agent.NewSwarm("handler", agent.SwarmOptions{})
	s.AddAgent("handler", &agent.SwarmAgent{
		Agent:       handler,
		Description: "Routes requests to the right specialist.",
	})
	s.AddAgent("specialist", &agent.SwarmAgent{
		Agent:       specialist,
		Description: "Handles specialized queries.",
	})

	resp, err := bond.Invoke(context.Background(), s, bond.TextPrompt("help me"), bond.AgentOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text)
	// Output: I'm the specialist. Here's your answer.
}

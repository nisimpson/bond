package bond_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

func ExampleInvoke() {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("Hello, ", "world!"),
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text)
	// Output: Hello, world!
}

func ExampleStream() {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("Bond", ". James ", "Bond."),
	}

	var text string
	for event, err := range bond.Stream(context.Background(), agent, bond.TextPrompt("name?"), bond.AgentOptions{}) {
		if err != nil {
			panic(err)
		}
		if event.Type == bond.StreamEventTextDelta {
			text += event.TextDelta
		}
	}
	fmt.Println(text)
	// Output: Bond. James Bond.
}

func ExampleNewFuncTool() {
	type AddInput struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	type AddOutput struct {
		Sum int `json:"sum"`
	}

	adder, _ := bond.NewFuncTool(
		func(ctx context.Context, in AddInput) (AddOutput, error) {
			return AddOutput{Sum: in.A + in.B}, nil
		},
		bond.FuncToolOptions{
			Name:        "add",
			Description: "Adds two numbers",
		},
	)

	// Simulate: model calls the tool, then responds with text
	agent := &bondtest.Agent{
		StreamFunc: bondtest.Sequence(
			bondtest.ToolUseEvents(&bond.ToolUseBlock{
				ID:    "call_1",
				Name:  "add",
				Input: json.RawMessage(`{"a":2,"b":3}`),
			}),
			bondtest.TextEvents("The sum is 5."),
		),
	}

	resp, err := bond.Invoke(context.Background(), agent, bond.TextPrompt("add 2+3"), bond.AgentOptions{
		Tools: []bond.Tool{adder},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text)
	// Output: The sum is 5.
}

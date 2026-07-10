//go:build examples

// Echo Server — serves a bondtest.EchoAgent across all AgentCore protocols.
//
// Run:
//
//	go run -tags examples ./examples/echoserver
//
// Ports:
//   - :9000 — A2A (JSON-RPC 2.0)
//   - :8080 — HTTP (POST /invocations)
//   - :8000 — MCP (streamable HTTP)
package main

import (
	"log"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
	"github.com/nisimpson/bond/runtime"
	"github.com/nisimpson/bond/runtime/agentcore"
)

func main() {
	agent := &bondtest.EchoAgent{}

	opts := agentcore.Options{
		Options: runtime.Options{
			Card: &a2a.AgentCard{
				Name:               "echo-007",
				Description:        "An echo agent. What you say is what you get.",
				Version:            "0.0.7",
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
				Capabilities: a2a.AgentCapabilities{
					Streaming: true,
				},
				Skills: []a2a.AgentSkill{
					{
						ID:          "echo",
						Name:        "Echo",
						Description: "Repeats whatever you send.",
					},
				},
			},
			AgentOptions: bond.AgentOptions{},
		},
	}

	log.Println("🍸 Bond Echo Server starting...")
	log.Println("   A2A  → :9000")
	log.Println("   HTTP → :8080")
	log.Println("   MCP  → :8000")

	if err := agentcore.Serve(agent, opts); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

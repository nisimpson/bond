//go:build examples

// ACP Client — demonstrates connecting to an external ACP agent (like kiro-cli)
// and sending prompts via the bond ACP proxy client.
//
// This example spawns the ACP agent as a subprocess via stdio, sends a prompt,
// and streams back the response (text deltas and tool use events).
//
// Run:
//
//	go run -tags examples ./examples/acpclient
//
// Environment:
//   - ACP_COMMAND: the command to spawn (default "kiro")
//   - ACP_ARGS: space-separated arguments (default "--acp")
//   - ACP_WORKDIR: working directory for the agent (default ".")
//   - ACP_PROMPT: the prompt to send (default "What files are in the current directory?")
//   - ACP_SYSTEM: optional system prompt
//   - ACP_TIER: permission tier — "yolo", "trust", or "read" (default "yolo")
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/agent/acpproxy"
	"github.com/nisimpson/bond/agent/acpproxy/acpio"
)

func main() {
	command := envOr("ACP_COMMAND", "kiro")
	args := strings.Fields(envOr("ACP_ARGS", "--acp"))
	workdir := envOr("ACP_WORKDIR", ".")
	prompt := envOr("ACP_PROMPT", "What files are in the current directory?")
	system := os.Getenv("ACP_SYSTEM")
	tier := parseTier(envOr("ACP_TIER", "yolo"))

	fmt.Printf("Command:  %s %s\n", command, strings.Join(args, " "))
	fmt.Printf("WorkDir:  %s\n", workdir)
	fmt.Printf("Tier:     %s\n", tierName(tier))
	if system != "" {
		fmt.Printf("System:   %s\n", truncate(system, 60))
	}
	fmt.Printf("Prompt:   %s\n", truncate(prompt, 80))
	fmt.Println("---")

	debug := os.Getenv("ACP_DEBUG") == "1"

	// Create and start the StdioProcess.
	proc := acpio.NewStdioProcess(command, args, acpio.StdioOptions{
		Stderr:  os.Stderr,
		Timeout: 10 * time.Second,
		Dir:     workdir,
	})
	if err := proc.Start(); err != nil {
		log.Fatalf("Failed to start ACP agent: %v", err)
	}

	// Wrap with debug logger if requested.
	var transport acpproxy.ReadWriter = proc
	if debug {
		transport = &debugReadWriter{rw: proc}
	}

	// Resolve to absolute path for the working directory.
	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		log.Fatalf("Failed to resolve working directory: %v", err)
	}

	// Create the ACP client using the StdioProcess as the ReadWriter.
	client := acpproxy.NewClient(transport, acpproxy.ClientOptions{
		WorkingDir:     absWorkdir,
		SystemPrompt:   system,
		PermissionTier: tier,
		CancelTimeout:  10 * time.Second,
	})

	// Set up context with signal handling for graceful cancellation.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Initialize the ACP protocol (handshake + session creation + priming).
	fmt.Println("[*] Initializing ACP session...")
	if err := client.Start(ctx); err != nil {
		log.Fatalf("ACP initialization failed: %v", err)
	}
	defer client.Close()

	info := client.AgentInfo()
	fmt.Printf("[*] Connected to %s v%s\n", info.Name, info.Version)
	fmt.Println("---")

	// Send the prompt and stream the response.
	agent := client.Agent()
	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: prompt}}},
	}

	for event, err := range agent.Stream(ctx, messages) {
		if err != nil {
			fmt.Printf("\n[!] Error: %v\n", err)
			break
		}
		switch event.Type {
		case bond.StreamEventTextDelta:
			fmt.Print(event.TextDelta)
		case bond.StreamEventToolUse:
			status := ""
			if event.Metadata != nil {
				if s, ok := event.Metadata["status"].(string); ok {
					status = s
				}
			}
			fmt.Printf("\n  [tool] %s (id=%s, status=%s)\n", event.ToolUse.Name, event.ToolUse.ID, status)
		case bond.StreamEventStop:
			fmt.Printf("\n---\n[*] Done (stop_reason: %s)\n", event.StopReason)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseTier(s string) acpproxy.PermissionTier {
	switch strings.ToLower(s) {
	case "read":
		return acpproxy.TierRead
	case "trust":
		return acpproxy.TierTrust
	default:
		return acpproxy.TierYOLO
	}
}

func tierName(t acpproxy.PermissionTier) string {
	switch t {
	case acpproxy.TierRead:
		return "read"
	case acpproxy.TierTrust:
		return "trust"
	default:
		return "yolo"
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// debugReadWriter wraps a ReadWriter and logs all messages to stderr.
type debugReadWriter struct {
	rw acpproxy.ReadWriter
}

func (d *debugReadWriter) ReadMessage() (json.RawMessage, error) {
	msg, err := d.rw.ReadMessage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] <<< READ ERROR: %v\n", err)
		return msg, err
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] <<< %s\n", string(msg))
	return msg, nil
}

func (d *debugReadWriter) WriteMessage(msg json.RawMessage) error {
	fmt.Fprintf(os.Stderr, "[DEBUG] >>> %s\n", string(msg))
	return d.rw.WriteMessage(msg)
}

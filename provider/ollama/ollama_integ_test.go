package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/ollama"
)

// skipIfNoOllama skips the test if Ollama is not reachable at the given base URL.
func skipIfNoOllama(t *testing.T, baseURL string) {
	t.Helper()
	if os.Getenv("BOND_INTEG_OLLAMA") == "" {
		t.Skip("skipping: set BOND_INTEG_OLLAMA=1 to run Ollama integration tests")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		t.Skipf("skipping: Ollama not reachable at %s: %v", baseURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("skipping: Ollama returned %d at %s/api/tags", resp.StatusCode, baseURL)
	}
}

// ollamaModel returns the model to use for integration tests.
// Defaults to "deepseek-r1:14b" but can be overridden via BOND_INTEG_OLLAMA_MODEL.
func ollamaModel() string {
	if m := os.Getenv("BOND_INTEG_OLLAMA_MODEL"); m != "" {
		return m
	}
	return "deepseek-r1:14b"
}

// TestIntegration_StreamText verifies that the Ollama provider can stream
// a basic text response from a live Ollama instance.
func TestIntegration_StreamText(t *testing.T) {
	baseURL := "http://localhost:11434"
	skipIfNoOllama(t, baseURL)

	agent := ollama.New(ollama.AgentOptions{
		Model:   ollamaModel(),
		BaseURL: baseURL,
		System:  "You are a helpful assistant. Keep answers very brief — one sentence max.",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := bond.Invoke(ctx, agent, bond.TextPrompt("What is 2+2? Answer with just the number."), bond.AgentOptions{})
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	if resp.Text == "" {
		t.Fatal("expected non-empty text response")
	}
	t.Logf("Response: %s", resp.Text)

	if resp.StopReason != bond.StopReasonEnd {
		t.Errorf("expected StopReason End, got %v", resp.StopReason)
	}
}

// TestIntegration_StreamWithTools verifies that the provider correctly handles
// tool use via the agent loop with a live Ollama instance.
func TestIntegration_StreamWithTools(t *testing.T) {
	baseURL := "http://localhost:11434"
	skipIfNoOllama(t, baseURL)

	agent := ollama.New(ollama.AgentOptions{
		Model:   ollamaModel(),
		BaseURL: baseURL,
		System:  "You are a helpful assistant. When asked about the weather, use the get_weather tool. Keep answers brief.",
	})

	weatherTool, err := bond.NewFuncTool(
		func(ctx context.Context, input struct {
			City string `json:"city"`
		}) (struct {
			Temperature string `json:"temperature"`
			Condition   string `json:"condition"`
		}, error) {
			return struct {
				Temperature string `json:"temperature"`
				Condition   string `json:"condition"`
			}{
				Temperature: "72°F",
				Condition:   "sunny",
			}, nil
		},
		bond.FuncToolOptions{
			Name:        "get_weather",
			Description: "Get the current weather for a city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","description":"The city name"}},"required":["city"]}`),
		},
	)
	if err != nil {
		t.Fatalf("NewFuncTool: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := bond.Invoke(ctx, agent, bond.TextPrompt("What's the weather in Seattle?"), bond.AgentOptions{
		Tools:    []bond.Tool{weatherTool},
		MaxTurns: 3,
	})
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	if resp.Text == "" {
		t.Fatal("expected non-empty text response")
	}
	t.Logf("Response: %s", resp.Text)
}

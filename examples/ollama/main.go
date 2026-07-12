//go:build examples

// Ollama Chat — demonstrates the Ollama provider with streaming text and tool use.
//
// Prerequisites:
//   - Ollama running locally (default: http://localhost:11434)
//   - A model pulled: ollama pull llama3
//
// Run:
//
//	go run -tags examples ./examples/ollama
//
// Environment:
//   - OLLAMA_MODEL: model name (default "llama3")
//   - OLLAMA_BASE_URL: Ollama endpoint (default "http://localhost:11434")
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/ollama"
	"github.com/nisimpson/bond/tool"
)

// WeatherInput is the expected input for the get_weather tool.
type WeatherInput struct {
	City string `json:"city" jsonschema:"The city name,required"`
}

// WeatherOutput is the response from the get_weather tool.
type WeatherOutput struct {
	Temperature string `json:"temperature"`
	Condition   string `json:"condition"`
}

// getWeather is the tool handler that returns mock weather data.
func getWeather(_ context.Context, input WeatherInput) (WeatherOutput, error) {
	fmt.Printf("  [tool] get_weather called for %q\n", input.City)
	return WeatherOutput{
		Temperature: "72°F",
		Condition:   "sunny",
	}, nil
}

func main() {
	model := envOr("OLLAMA_MODEL", "llama3")
	baseURL := envOr("OLLAMA_BASE_URL", "http://localhost:11434")

	agent := ollama.New(ollama.AgentOptions{
		Model:   model,
		BaseURL: baseURL,
		System:  "You are a helpful assistant. When asked about the weather, use the get_weather tool.",
	})

	weatherTool, err := bond.NewFuncTool(getWeather, bond.FuncToolOptions{
		Name:        "get_weather",
		Description: "Get the current weather for a city",
		InputSchema: tool.SchemaFor[WeatherInput](),
	})
	if err != nil {
		log.Fatalf("NewFuncTool: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("Model: %s @ %s\n", model, baseURL)
	fmt.Println("Prompt: What's the weather like in Seattle?")
	fmt.Println("---")

	// Stream events to see incremental output.
	for event, err := range bond.Stream(ctx, agent, bond.TextPrompt("What's the weather like in Seattle?"), bond.AgentOptions{
		Tools:    []bond.Tool{weatherTool},
		MaxTurns: 3,
	}) {
		if err != nil {
			log.Fatalf("Stream error: %v", err)
		}
		switch event.Type {
		case bond.StreamEventTextDelta:
			fmt.Print(event.TextDelta)
		case bond.StreamEventToolUse:
			fmt.Printf("\n  [tool_use] %s(%s)\n", event.ToolUse.Name, string(event.ToolUse.Input))
		case bond.StreamEventStop:
			fmt.Printf("\n---\nStop reason: %s\n", event.StopReason)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

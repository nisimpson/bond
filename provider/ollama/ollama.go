// Package ollama provides a bond.Agent implementation backed by
// OpenAI-compatible Chat Completions endpoints (Ollama, LiteLLM, vLLM).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/internal/openai"
)

// Requirement: 1.1 — compile-time interface check
var _ bond.Agent = (*Agent)(nil)

// Requirement: 1.2 — AgentOptions with configurable fields
// AgentOptions configures the Ollama agent.
type AgentOptions struct {
	// Model is the model name (e.g. "llama3", "mistral").
	Model string
	// BaseURL is the root URL of the service. Default: "http://localhost:11434".
	BaseURL string
	// System is the system prompt prepended to every request.
	System string
	// APIKey is an optional Bearer token for authenticated endpoints.
	APIKey string
	// HTTPClient overrides the default http.Client.
	HTTPClient *http.Client
	// Temperature is an optional inference parameter.
	Temperature *float64
	// MaxTokens is an optional inference parameter.
	MaxTokens *int
	// TopP is an optional inference parameter.
	TopP *float64
}

// Agent implements bond.Agent using an OpenAI-compatible endpoint.
type Agent struct {
	opts   AgentOptions
	client *http.Client
}

// New creates an Ollama-backed bond.Agent.
func New(opts AgentOptions) *Agent {
	// Requirement: 1.3 — default BaseURL to localhost:11434
	if opts.BaseURL == "" {
		opts.BaseURL = "http://localhost:11434"
	}
	// Requirement: 1.4, 1.5 — use provided or default HTTP client
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Agent{opts: opts, client: client}
}

// Stream implements bond.Agent.
func (a *Agent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		// Requirement: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6 — map messages to OpenAI format
		oaiMessages := openai.MapMessages(messages, a.opts.System)

		// Requirement: 3.1, 3.2, 3.3 — map tools from context
		tools, err := openai.MapTools(bond.ToolsFromContext(ctx))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("ollama: %w", err))
			return
		}

		// Requirement: 4.2, 8.3 — build request body with stream=true and optional params
		reqBody := openai.ChatCompletionRequest{
			Model:       a.opts.Model,
			Messages:    oaiMessages,
			Stream:      true,
			Tools:       tools,
			Temperature: a.opts.Temperature,
			MaxTokens:   a.opts.MaxTokens,
			TopP:        a.opts.TopP,
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("ollama: marshal request: %w", err))
			return
		}

		// Requirement: 4.1 — POST to {BaseURL}/v1/chat/completions
		url := a.opts.BaseURL + "/v1/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("ollama: create request: %w", err))
			return
		}

		// Requirement: 4.1 — set Content-Type header
		req.Header.Set("Content-Type", "application/json")

		// Requirement: 4.3 — set Authorization Bearer header if API key configured
		if a.opts.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+a.opts.APIKey)
		}

		// Requirement: 6.1 — handle network errors
		resp, err := a.client.Do(req)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("ollama: %w", err))
			return
		}

		// Requirement: 6.2 — handle non-2xx responses with body excerpt
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			excerpt := readExcerpt(resp.Body, 512)
			resp.Body.Close()
			yield(bond.StreamEvent{}, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, excerpt))
			return
		}

		// Requirement: 6.5 — delegate SSE parsing with "ollama:" error prefix
		// StreamReader takes ownership of resp.Body and closes it via defer.
		openai.StreamReader(ctx, resp.Body, "ollama:", yield)
	}
}

// readExcerpt reads up to maxBytes from r for inclusion in error messages.
func readExcerpt(r io.Reader, maxBytes int) string {
	buf := make([]byte, maxBytes)
	n, _ := io.ReadAtLeast(r, buf, 1)
	if n == 0 {
		return "<empty body>"
	}
	return string(buf[:n])
}

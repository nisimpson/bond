// Package anthropic provides a bond.Agent implementation backed by the
// Anthropic Messages API.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"

	"github.com/nisimpson/bond"
	internal "github.com/nisimpson/bond/provider/internal/anthropic"
)

var _ bond.Agent = (*Agent)(nil)

// AgentOptions configures the Anthropic agent.
type AgentOptions struct {
	// Model is the model name (e.g. "claude-sonnet-4-20250514").
	Model string
	// BaseURL is the root URL of the API. Default: "https://api.anthropic.com".
	BaseURL string
	// System is the system prompt sent as a top-level field on every request.
	System string
	// APIKey is the Anthropic API key sent via x-api-key header (required).
	APIKey string
	// HTTPClient overrides the default http.Client.
	HTTPClient *http.Client
	// Temperature is an optional inference parameter.
	Temperature *float64
	// MaxTokens is the max tokens to generate. Default: 4096.
	MaxTokens int
	// TopP is an optional inference parameter.
	TopP *float64
}

// Agent implements bond.Agent using the Anthropic Messages API.
type Agent struct {
	opts   AgentOptions
	client *http.Client
}

// New creates an Anthropic-backed bond.Agent.
func New(opts AgentOptions) *Agent {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.anthropic.com"
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 4096
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Agent{opts: opts, client: client}
}

// Stream implements bond.Agent.
func (a *Agent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		msgs := internal.MapMessages(messages)

		tools, err := internal.MapTools(bond.ToolsFromContext(ctx))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("anthropic: %w", err))
			return
		}

		reqBody := internal.MessagesRequest{
			Model:       a.opts.Model,
			Messages:    msgs,
			MaxTokens:   a.opts.MaxTokens,
			Stream:      true,
			Tools:       tools,
			Temperature: a.opts.Temperature,
			TopP:        a.opts.TopP,
		}

		if a.opts.System != "" {
			reqBody.System = a.opts.System
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("anthropic: marshal request: %w", err))
			return
		}

		url := a.opts.BaseURL + "/v1/messages"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("anthropic: create request: %w", err))
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.opts.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := a.client.Do(req)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("anthropic: %w", err))
			return
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			excerpt := readExcerpt(resp.Body, 512)
			resp.Body.Close()
			yield(bond.StreamEvent{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, excerpt))
			return
		}

		internal.StreamReader(ctx, resp.Body, yield)
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

// Package openai provides a bond.Agent implementation backed by the
// OpenAI Chat Completions API (https://api.openai.com).
package openai

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

var _ bond.Agent = (*Agent)(nil)

// AgentOptions configures the OpenAI agent.
type AgentOptions struct {
	// Model is the model name (e.g. "gpt-4o", "gpt-4o-mini").
	Model string
	// BaseURL is the root URL of the API. Default: "https://api.openai.com".
	BaseURL string
	// System is the system prompt prepended to every request.
	System string
	// APIKey is the Bearer token for authentication (required for OpenAI).
	APIKey string
	// Organization is an optional OpenAI organization ID sent via header.
	Organization string
	// HTTPClient overrides the default http.Client.
	HTTPClient *http.Client
	// Temperature is an optional inference parameter.
	Temperature *float64
	// MaxTokens is an optional inference parameter.
	MaxTokens *int
	// TopP is an optional inference parameter.
	TopP *float64
}

// Agent implements bond.Agent using the OpenAI Chat Completions API.
type Agent struct {
	opts   AgentOptions
	client *http.Client
}

// New creates an OpenAI-backed bond.Agent.
func New(opts AgentOptions) *Agent {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.openai.com"
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
		oaiMessages := openai.MapMessages(messages, a.opts.System)

		tools, err := openai.MapTools(bond.ToolsFromContext(ctx))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("openai: %w", err))
			return
		}

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
			yield(bond.StreamEvent{}, fmt.Errorf("openai: marshal request: %w", err))
			return
		}

		url := a.opts.BaseURL + "/v1/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("openai: create request: %w", err))
			return
		}

		req.Header.Set("Content-Type", "application/json")

		if a.opts.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+a.opts.APIKey)
		}

		if a.opts.Organization != "" {
			req.Header.Set("OpenAI-Organization", a.opts.Organization)
		}

		resp, err := a.client.Do(req)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("openai: %w", err))
			return
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			excerpt := readExcerpt(resp.Body, 512)
			resp.Body.Close()
			yield(bond.StreamEvent{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, excerpt))
			return
		}

		// StreamReader takes ownership of resp.Body and closes it via defer.
		openai.StreamReader(ctx, resp.Body, "openai:", yield)
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

// Package gemini provides a bond.Agent implementation backed by the Google
// Gemini Generative Language API.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"

	"github.com/nisimpson/bond"
	internal "github.com/nisimpson/bond/provider/internal/gemini"
)

var _ bond.Agent = (*Agent)(nil)

// AgentOptions configures the Gemini agent.
type AgentOptions struct {
	// Model is the model name (e.g. "gemini-2.0-flash").
	Model string
	// BaseURL is the root URL of the API. Default: "https://generativelanguage.googleapis.com".
	BaseURL string
	// System is the system prompt sent as a systemInstruction field.
	System string
	// APIKey is the Gemini API key sent via query parameter.
	APIKey string
	// OAuthToken is a Bearer token used when APIKey is empty.
	OAuthToken string
	// HTTPClient overrides the default http.Client.
	HTTPClient *http.Client
	// Temperature is an optional inference parameter.
	Temperature *float64
	// MaxOutputTokens is the max tokens to generate (optional).
	MaxOutputTokens *int
	// TopP is an optional inference parameter.
	TopP *float64
}

// Agent implements bond.Agent using the Gemini Generative Language API.
type Agent struct {
	opts   AgentOptions
	client *http.Client
}

// New creates a Gemini-backed bond.Agent.
func New(opts AgentOptions) *Agent {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://generativelanguage.googleapis.com"
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
		contents := internal.MapMessages(messages)

		tools, err := internal.MapTools(bond.ToolsFromContext(ctx))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: %w", err))
			return
		}

		reqBody := internal.GenerateContentRequest{
			Contents: contents,
			Tools:    tools,
		}

		if a.opts.System != "" {
			reqBody.SystemInstruction = &internal.Content{
				Parts: []internal.Part{{Text: a.opts.System}},
			}
		}

		if a.opts.Temperature != nil || a.opts.MaxOutputTokens != nil || a.opts.TopP != nil {
			reqBody.GenerationConfig = &internal.GenerationConfig{
				Temperature:     a.opts.Temperature,
				MaxOutputTokens: a.opts.MaxOutputTokens,
				TopP:            a.opts.TopP,
			}
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: marshal request: %w", err))
			return
		}

		url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse",
			a.opts.BaseURL, a.opts.Model)

		if a.opts.APIKey != "" {
			url += "&key=" + a.opts.APIKey
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: create request: %w", err))
			return
		}

		req.Header.Set("Content-Type", "application/json")

		if a.opts.OAuthToken != "" && a.opts.APIKey == "" {
			req.Header.Set("Authorization", "Bearer "+a.opts.OAuthToken)
		}

		resp, err := a.client.Do(req)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: %w", err))
			return
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			excerpt := readExcerpt(resp.Body, 512)
			resp.Body.Close()
			yield(bond.StreamEvent{}, fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, excerpt))
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

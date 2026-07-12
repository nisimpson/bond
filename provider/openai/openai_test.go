package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/quick"

	bond "github.com/nisimpson/bond"
)

type mockTransport struct {
	handler func(*http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.handler(req)
}

func buildTestSSEBody(chunks ...string) string {
	var sb strings.Builder
	for _, chunk := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(chunk)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

type netError struct{ msg string }

func (e *netError) Error() string { return e.msg }

func TestNew_Defaults(t *testing.T) {
	agent := New(AgentOptions{Model: "gpt-4o"})

	if agent.opts.BaseURL != "https://api.openai.com" {
		t.Errorf("expected default BaseURL 'https://api.openai.com', got %q", agent.opts.BaseURL)
	}
	if agent.client != http.DefaultClient {
		t.Error("expected default client to be http.DefaultClient")
	}
}

func TestNew_CustomOptions(t *testing.T) {
	customClient := &http.Client{}
	temp := 0.7
	maxTokens := 1024
	topP := 0.9

	agent := New(AgentOptions{
		Model:        "gpt-4o-mini",
		BaseURL:      "https://custom.openai.com",
		System:       "You are helpful",
		APIKey:       "sk-test-key",
		Organization: "org-abc123",
		HTTPClient:   customClient,
		Temperature:  &temp,
		MaxTokens:    &maxTokens,
		TopP:         &topP,
	})

	if agent.opts.Model != "gpt-4o-mini" {
		t.Errorf("expected model 'gpt-4o-mini', got %q", agent.opts.Model)
	}
	if agent.opts.BaseURL != "https://custom.openai.com" {
		t.Errorf("expected BaseURL 'https://custom.openai.com', got %q", agent.opts.BaseURL)
	}
	if agent.opts.Organization != "org-abc123" {
		t.Errorf("expected Organization 'org-abc123', got %q", agent.opts.Organization)
	}
	if agent.client != customClient {
		t.Error("expected custom client to be used")
	}
}

func TestStream_NetworkError(t *testing.T) {
	agent := New(AgentOptions{
		Model: "gpt-4o",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					return nil, &netError{msg: "connection refused"}
				},
			},
		},
	})

	var gotErr error
	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hello")) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected an error")
	}
	if !strings.HasPrefix(gotErr.Error(), "openai:") {
		t.Errorf("expected error to start with 'openai:', got %q", gotErr.Error())
	}
}

func TestStream_HTTPError(t *testing.T) {
	agent := New(AgentOptions{
		Model: "gpt-4o",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: 429,
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limit exceeded"}}`)),
					}, nil
				},
			},
		},
	})

	var gotErr error
	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hello")) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected an error")
	}
	if !strings.HasPrefix(gotErr.Error(), "openai:") {
		t.Errorf("expected error to start with 'openai:', got %q", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "HTTP 429") {
		t.Errorf("expected error to contain 'HTTP 429', got %q", gotErr.Error())
	}
}

func TestStream_WithAPIKeyAndOrganization(t *testing.T) {
	var capturedReq *http.Request

	agent := New(AgentOptions{
		Model:        "gpt-4o",
		APIKey:       "sk-my-secret",
		Organization: "org-test",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					capturedReq = req
					body := buildTestSSEBody(`{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				},
			},
		},
	})

	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if got := capturedReq.Header.Get("Authorization"); got != "Bearer sk-my-secret" {
		t.Errorf("expected Authorization 'Bearer sk-my-secret', got %q", got)
	}
	if got := capturedReq.Header.Get("OpenAI-Organization"); got != "org-test" {
		t.Errorf("expected OpenAI-Organization 'org-test', got %q", got)
	}
}

func TestStream_NoOrganizationHeader(t *testing.T) {
	var capturedReq *http.Request

	agent := New(AgentOptions{
		Model:  "gpt-4o",
		APIKey: "sk-key",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					capturedReq = req
					body := buildTestSSEBody(`{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				},
			},
		},
	})

	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if capturedReq.Header.Get("OpenAI-Organization") != "" {
		t.Errorf("expected no OpenAI-Organization header when not configured, got %q", capturedReq.Header.Get("OpenAI-Organization"))
	}
}

func TestStream_OptionalParams(t *testing.T) {
	var capturedBody []byte
	temp := 0.5
	maxTokens := 4096
	topP := 0.95

	agent := New(AgentOptions{
		Model:       "gpt-4o",
		APIKey:      "sk-key",
		Temperature: &temp,
		MaxTokens:   &maxTokens,
		TopP:        &topP,
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					capturedBody, _ = io.ReadAll(req.Body)
					body := buildTestSSEBody(`{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				},
			},
		},
	})

	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	var reqBody map[string]any
	if err := json.Unmarshal(capturedBody, &reqBody); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}

	if v, ok := reqBody["temperature"]; !ok || v.(float64) != 0.5 {
		t.Errorf("expected temperature 0.5, got %v", reqBody["temperature"])
	}
	if v, ok := reqBody["max_tokens"]; !ok || v.(float64) != 4096 {
		t.Errorf("expected max_tokens 4096, got %v", reqBody["max_tokens"])
	}
	if v, ok := reqBody["top_p"]; !ok || v.(float64) != 0.95 {
		t.Errorf("expected top_p 0.95, got %v", reqBody["top_p"])
	}
}

func TestStream_EndToEnd(t *testing.T) {
	chunks := []string{
		`{"id":"ch1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
		`{"id":"ch2","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
	}
	sseBody := buildTestSSEBody(chunks...)

	agent := New(AgentOptions{
		Model:  "gpt-4o",
		APIKey: "sk-key",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(sseBody)),
					}, nil
				},
			},
		},
	})

	var events []bond.StreamEvent
	for event, err := range agent.Stream(context.Background(), bond.TextPrompt("hi")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d", len(events))
	}
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("expected Start, got %v", events[0].Type)
	}
	if events[1].TextDelta != "Hello" {
		t.Errorf("expected 'Hello', got %q", events[1].TextDelta)
	}
	if events[2].TextDelta != " world" {
		t.Errorf("expected ' world', got %q", events[2].TextDelta)
	}
	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop || last.StopReason != bond.StopReasonEnd {
		t.Errorf("expected Stop/End, got %v/%v", last.Type, last.StopReason)
	}
}

func TestProperty_ErrorPrefixConsistency(t *testing.T) {
	f := func(statusCode uint8, errMsg string) bool {
		if errMsg == "" {
			errMsg = "error"
		}

		// Network errors
		networkAgent := New(AgentOptions{
			Model: "test",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return nil, &netError{msg: errMsg}
					},
				},
			},
		})

		for _, err := range networkAgent.Stream(context.Background(), bond.TextPrompt("hi")) {
			if err != nil {
				if !strings.HasPrefix(err.Error(), "openai:") {
					return false
				}
				break
			}
		}

		// HTTP errors
		code := int(statusCode)%200 + 400
		httpAgent := New(AgentOptions{
			Model: "test",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: code,
							Body:       io.NopCloser(strings.NewReader(errMsg)),
						}, nil
					},
				},
			},
		})

		for _, err := range httpAgent.Stream(context.Background(), bond.TextPrompt("hi")) {
			if err != nil {
				if !strings.HasPrefix(err.Error(), "openai:") {
					return false
				}
				break
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("error prefix consistency property failed: %v", err)
	}
}

package ollama

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

// mockTransport is a test RoundTripper returning canned responses.
type mockTransport struct {
	handler func(*http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.handler(req)
}

// buildTestSSEBody constructs a synthetic SSE body string from JSON chunks.
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

// --------------------------------------------------------------------------
// Tests for New()
// Validates: Requirements 1.1, 1.2, 1.3, 1.4, 1.5
// --------------------------------------------------------------------------

func TestNew_Defaults(t *testing.T) {
	agent := New(AgentOptions{Model: "llama3"})

	if agent.opts.BaseURL != "http://localhost:11434" {
		t.Errorf("expected default BaseURL 'http://localhost:11434', got %q", agent.opts.BaseURL)
	}
	if agent.client != http.DefaultClient {
		t.Errorf("expected default client to be http.DefaultClient")
	}
}

func TestNew_CustomOptions(t *testing.T) {
	customClient := &http.Client{}
	temp := 0.7
	maxTokens := 1024
	topP := 0.9

	agent := New(AgentOptions{
		Model:       "mistral",
		BaseURL:     "http://my-server:8080",
		System:      "You are helpful",
		APIKey:      "sk-test-key",
		HTTPClient:  customClient,
		Temperature: &temp,
		MaxTokens:   &maxTokens,
		TopP:        &topP,
	})

	if agent.opts.Model != "mistral" {
		t.Errorf("expected model 'mistral', got %q", agent.opts.Model)
	}
	if agent.opts.BaseURL != "http://my-server:8080" {
		t.Errorf("expected BaseURL 'http://my-server:8080', got %q", agent.opts.BaseURL)
	}
	if agent.opts.System != "You are helpful" {
		t.Errorf("expected system prompt 'You are helpful', got %q", agent.opts.System)
	}
	if agent.opts.APIKey != "sk-test-key" {
		t.Errorf("expected APIKey 'sk-test-key', got %q", agent.opts.APIKey)
	}
	if agent.client != customClient {
		t.Error("expected custom client to be used")
	}
	if agent.opts.Temperature == nil || *agent.opts.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", agent.opts.Temperature)
	}
	if agent.opts.MaxTokens == nil || *agent.opts.MaxTokens != 1024 {
		t.Errorf("expected max_tokens 1024, got %v", agent.opts.MaxTokens)
	}
	if agent.opts.TopP == nil || *agent.opts.TopP != 0.9 {
		t.Errorf("expected top_p 0.9, got %v", agent.opts.TopP)
	}
}

// --------------------------------------------------------------------------
// Tests for Stream()
// Validates: Requirements 4.1, 4.2, 4.3, 6.1, 6.2, 6.5, 8.1, 8.2, 8.3
// --------------------------------------------------------------------------

// Validates: Requirements 6.1, 6.5
func TestStream_NetworkError(t *testing.T) {
	agent := New(AgentOptions{
		Model: "llama3",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					return nil, &net_error{msg: "connection refused"}
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
	if !strings.HasPrefix(gotErr.Error(), "ollama:") {
		t.Errorf("expected error to start with 'ollama:', got %q", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "connection refused") {
		t.Errorf("expected error to contain 'connection refused', got %q", gotErr.Error())
	}
}

// Validates: Requirements 6.2, 6.5
func TestStream_HTTPError(t *testing.T) {
	agent := New(AgentOptions{
		Model: "llama3",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: 500,
						Body:       io.NopCloser(strings.NewReader("internal server error")),
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
	if !strings.HasPrefix(gotErr.Error(), "ollama:") {
		t.Errorf("expected error to start with 'ollama:', got %q", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "HTTP 500") {
		t.Errorf("expected error to contain 'HTTP 500', got %q", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "internal server error") {
		t.Errorf("expected error to contain body excerpt, got %q", gotErr.Error())
	}
}

// Validates: Requirements 4.3, 8.2
func TestStream_WithAPIKey(t *testing.T) {
	var capturedReq *http.Request

	agent := New(AgentOptions{
		Model:  "llama3",
		APIKey: "sk-my-secret",
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
	authHeader := capturedReq.Header.Get("Authorization")
	if authHeader != "Bearer sk-my-secret" {
		t.Errorf("expected Authorization 'Bearer sk-my-secret', got %q", authHeader)
	}
}

// Validates: Requirements 3.3
func TestStream_NoTools(t *testing.T) {
	var capturedBody []byte

	agent := New(AgentOptions{
		Model: "llama3",
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

	// Stream with no tools in context
	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Verify the request body has no "tools" field (omitempty should skip it)
	var reqBody map[string]any
	if err := json.Unmarshal(capturedBody, &reqBody); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	if _, exists := reqBody["tools"]; exists {
		t.Errorf("expected 'tools' to be omitted from request body when no tools available")
	}
}

// Validates: Requirements 8.3
func TestStream_OptionalParams(t *testing.T) {
	var capturedBody []byte
	temp := 0.5
	maxTokens := 2048
	topP := 0.95

	agent := New(AgentOptions{
		Model:       "llama3",
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

	if v, ok := reqBody["temperature"]; !ok {
		t.Error("expected 'temperature' in request body")
	} else if v.(float64) != 0.5 {
		t.Errorf("expected temperature 0.5, got %v", v)
	}

	if v, ok := reqBody["max_tokens"]; !ok {
		t.Error("expected 'max_tokens' in request body")
	} else if v.(float64) != 2048 {
		t.Errorf("expected max_tokens 2048, got %v", v)
	}

	if v, ok := reqBody["top_p"]; !ok {
		t.Error("expected 'top_p' in request body")
	} else if v.(float64) != 0.95 {
		t.Errorf("expected top_p 0.95, got %v", v)
	}
}

// Validates: Requirements 1.1, 4.1, 4.2, 5.1, 5.2, 5.3, 5.4, 5.5
func TestStream_EndToEnd(t *testing.T) {
	// Build SSE stream: text + tool call fragments + finish_reason
	chunks := []string{
		`{"id":"ch1","choices":[{"index":0,"delta":{"role":"assistant","content":"Let me "},"finish_reason":null}]}`,
		`{"id":"ch2","choices":[{"index":0,"delta":{"content":"check."},"finish_reason":null}]}`,
		`{"id":"ch3","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"ch4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
		`{"id":"ch5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"NYC\"}"}}]},"finish_reason":"tool_calls"}]}`,
	}
	sseBody := buildTestSSEBody(chunks...)

	var capturedReq *http.Request
	agent := New(AgentOptions{
		Model: "llama3",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				handler: func(req *http.Request) (*http.Response, error) {
					capturedReq = req
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(sseBody)),
					}, nil
				},
			},
		},
	})

	var events []bond.StreamEvent
	for event, err := range agent.Stream(context.Background(), bond.TextPrompt("what's the weather?")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	// Verify request was sent to correct endpoint
	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	expectedURL := "http://localhost:11434/v1/chat/completions"
	if capturedReq.URL.String() != expectedURL {
		t.Errorf("expected request URL %q, got %q", expectedURL, capturedReq.URL.String())
	}
	if capturedReq.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", capturedReq.Header.Get("Content-Type"))
	}

	// Verify stream=true in body
	bodyBytes, _ := io.ReadAll(capturedReq.Body)
	var reqBody map[string]any
	if err := json.Unmarshal(bodyBytes, &reqBody); err == nil {
		if stream, ok := reqBody["stream"]; !ok || stream != true {
			t.Errorf("expected stream=true in request body, got %v", reqBody["stream"])
		}
	}

	// Verify event sequence
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events (Start, TextDelta×2, ToolUse, Stop), got %d", len(events))
	}

	// First event: Start
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("expected first event to be Start, got %v", events[0].Type)
	}

	// Text deltas
	if events[1].Type != bond.StreamEventTextDelta || events[1].TextDelta != "Let me " {
		t.Errorf("expected TextDelta 'Let me ', got %v %q", events[1].Type, events[1].TextDelta)
	}
	if events[2].Type != bond.StreamEventTextDelta || events[2].TextDelta != "check." {
		t.Errorf("expected TextDelta 'check.', got %v %q", events[2].Type, events[2].TextDelta)
	}

	// ToolUse event
	var toolUseEvent *bond.StreamEvent
	for i := range events {
		if events[i].Type == bond.StreamEventToolUse {
			toolUseEvent = &events[i]
			break
		}
	}
	if toolUseEvent == nil {
		t.Fatal("expected a ToolUse event")
	}
	if toolUseEvent.ToolUse.ID != "call_abc" {
		t.Errorf("expected tool use ID 'call_abc', got %q", toolUseEvent.ToolUse.ID)
	}
	if toolUseEvent.ToolUse.Name != "get_weather" {
		t.Errorf("expected tool use name 'get_weather', got %q", toolUseEvent.ToolUse.Name)
	}
	expectedArgs := `{"city":"NYC"}`
	if string(toolUseEvent.ToolUse.Input) != expectedArgs {
		t.Errorf("expected tool use input %q, got %q", expectedArgs, string(toolUseEvent.ToolUse.Input))
	}

	// Stop event
	lastEvent := events[len(events)-1]
	if lastEvent.Type != bond.StreamEventStop {
		t.Errorf("expected last event to be Stop, got %v", lastEvent.Type)
	}
	if lastEvent.StopReason != bond.StopReasonToolUse {
		t.Errorf("expected StopReason ToolUse, got %v", lastEvent.StopReason)
	}
}

// --------------------------------------------------------------------------
// Property test: Error prefix consistency
// Property 7: All errors yielded by the provider begin with "ollama:"
// Validates: Requirements 6.1, 6.2, 6.5
// --------------------------------------------------------------------------

func TestProperty_ErrorPrefixConsistency(t *testing.T) {
	// Property: for any error returned by Stream (network error, HTTP error),
	// the error message always starts with "ollama:".
	f := func(statusCode uint8, errMsg string) bool {
		// Avoid empty error messages for the network error case
		if errMsg == "" {
			errMsg = "error"
		}

		// Test 1: Network errors have "ollama:" prefix
		networkAgent := New(AgentOptions{
			Model: "test",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return nil, &net_error{msg: errMsg}
					},
				},
			},
		})

		for _, err := range networkAgent.Stream(context.Background(), bond.TextPrompt("hi")) {
			if err != nil {
				if !strings.HasPrefix(err.Error(), "ollama:") {
					return false
				}
				break
			}
		}

		// Test 2: HTTP errors have "ollama:" prefix
		// Ensure non-2xx status by mapping to 400-599 range
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
				if !strings.HasPrefix(err.Error(), "ollama:") {
					return false
				}
				break
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 7 (error prefix consistency) failed: %v", err)
	}
}

// net_error is a simple error type for simulating network errors.
type net_error struct {
	msg string
}

func (e *net_error) Error() string { return e.msg }

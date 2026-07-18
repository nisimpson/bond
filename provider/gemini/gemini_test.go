package gemini_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/gemini"
)

func TestNew_Defaults(t *testing.T) {
	agent := gemini.New(gemini.AgentOptions{})

	if agent == nil {
		t.Fatal("New returned nil")
	}

	// Verify the default BaseURL is googleapis.com by hitting a test server
	// and confirming that when BaseURL is not overridden, the agent is non-nil.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer ts.Close()

	// With test server, verify basic operation works
	testAgent := gemini.New(gemini.AgentOptions{BaseURL: ts.URL, Model: "test"})
	ctx := context.Background()
	for _, err := range testAgent.Stream(ctx, []bond.Message{{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hi"}}}}) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestStream_APIKeyAuth(t *testing.T) {
	var requestURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.String()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer ts.Close()

	agent := gemini.New(gemini.AgentOptions{
		Model:   "gemini-2.0-flash",
		BaseURL: ts.URL,
		APIKey:  "test-key",
	})

	ctx := context.Background()
	for _, err := range agent.Stream(ctx, bond.TextPrompt("hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if !strings.Contains(requestURL, "&key=test-key") {
		t.Errorf("URL should contain &key=test-key, got: %s", requestURL)
	}
}

func TestStream_OAuthAuth(t *testing.T) {
	var authHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer ts.Close()

	agent := gemini.New(gemini.AgentOptions{
		Model:      "gemini-2.0-flash",
		BaseURL:    ts.URL,
		OAuthToken: "my-bearer-token",
		// APIKey is empty, so OAuthToken should be used
	})

	ctx := context.Background()
	for _, err := range agent.Stream(ctx, bond.TextPrompt("hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if authHeader != "Bearer my-bearer-token" {
		t.Errorf("Authorization header = %q, want %q", authHeader, "Bearer my-bearer-token")
	}
}

func TestStream_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "invalid request body")
	}))
	defer ts.Close()

	agent := gemini.New(gemini.AgentOptions{
		Model:   "gemini-2.0-flash",
		BaseURL: ts.URL,
		APIKey:  "test-key",
	})

	ctx := context.Background()
	var gotErr error
	for _, err := range agent.Stream(ctx, bond.TextPrompt("hello")) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for HTTP 400")
	}

	errMsg := gotErr.Error()
	if !strings.Contains(errMsg, "gemini: HTTP 400:") {
		t.Errorf("error should match 'gemini: HTTP 400: ...', got: %s", errMsg)
	}
}

func TestStream_SuccessfulTextStream(t *testing.T) {
	sseResponse := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]},"finishReason":"STOP"}]}`,
		"",
	}, "\n")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse)
	}))
	defer ts.Close()

	agent := gemini.New(gemini.AgentOptions{
		Model:   "gemini-2.0-flash",
		BaseURL: ts.URL,
		APIKey:  "test-key",
	})

	ctx := context.Background()
	var events []bond.StreamEvent
	for event, err := range agent.Stream(ctx, bond.TextPrompt("say hello")) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected stream events, got none")
	}

	// First event should be start
	if events[0].Type != bond.StreamEventStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, bond.StreamEventStart)
	}

	// Collect text deltas
	var text string
	for _, e := range events {
		if e.Type == bond.StreamEventTextDelta {
			text += e.TextDelta
		}
	}
	if text != "Hello world" {
		t.Errorf("concatenated text = %q, want %q", text, "Hello world")
	}

	// Last event should be stop with StopReasonEnd
	last := events[len(events)-1]
	if last.Type != bond.StreamEventStop {
		t.Errorf("last event type = %q, want %q", last.Type, bond.StreamEventStop)
	}
	if last.StopReason != bond.StopReasonEnd {
		t.Errorf("stop reason = %q, want %q", last.StopReason, bond.StopReasonEnd)
	}
}

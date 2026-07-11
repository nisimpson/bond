package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bond "github.com/nisimpson/bond"
)

func runHTTPTool(t *testing.T, tool bond.Tool, input HTTPInput) (HTTPOutput, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	blocks, err := tool.Run(context.Background(), raw)
	if err != nil {
		return HTTPOutput{}, err
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one block, got none")
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatalf("expected *bond.TextBlock, got %T", blocks[0])
	}
	var out HTTPOutput
	if err := json.Unmarshal([]byte(tb.Text), &out); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	return out, nil
}

// Validates: TBOX-2.1, TBOX-2.2
func TestHTTPTool_GETReturnsStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()

	tool := newHTTPTool(30 * time.Second)
	out, err := runHTTPTool(t, tool, HTTPInput{URL: server.URL, Method: "GET"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", out.StatusCode)
	}
	if out.Body != "hello world" {
		t.Errorf("expected body %q, got %q", "hello world", out.Body)
	}
}

// Validates: TBOX-2.2, TBOX-2.5
func TestHTTPTool_POSTSendsBody(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	defer server.Close()

	tool := newHTTPTool(30 * time.Second)
	out, err := runHTTPTool(t, tool, HTTPInput{
		URL:    server.URL,
		Method: "POST",
		Body:   `{"key":"value"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", out.StatusCode)
	}
	if receivedBody != `{"key":"value"}` {
		t.Errorf("expected body %q, got %q", `{"key":"value"}`, receivedBody)
	}
}

// Validates: TBOX-2.4
func TestHTTPTool_HeadersIncludedInRequest(t *testing.T) {
	var receivedAuth string
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	tool := newHTTPTool(30 * time.Second)
	_, err := runHTTPTool(t, tool, HTTPInput{
		URL:    server.URL,
		Method: "GET",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedAuth != "Bearer token123" {
		t.Errorf("expected Authorization %q, got %q", "Bearer token123", receivedAuth)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type %q, got %q", "application/json", receivedContentType)
	}
}

// Validates: TBOX-2.3
func TestHTTPTool_UnsupportedMethodRejected(t *testing.T) {
	tool := newHTTPTool(30 * time.Second)
	methods := []string{"PUT", "DELETE", "PATCH", "OPTIONS"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			raw, _ := json.Marshal(HTTPInput{URL: "http://example.com", Method: method})
			_, err := tool.Run(context.Background(), raw)
			if err == nil {
				t.Fatal("expected error for unsupported method, got nil")
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("expected ErrValidation, got: %v", err)
			}
		})
	}
}

// Validates: TBOX-2.6, TBOX-2.7
func TestHTTPTool_TimeoutProducesTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tool := newHTTPTool(100 * time.Millisecond)
	raw, _ := json.Marshal(HTTPInput{URL: server.URL, Method: "GET"})
	_, err := tool.Run(context.Background(), raw)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

// Validates: TBOX-2.8
func TestHTTPTool_4xxAnd5xxResponsesReturned(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte("error body"))
			}))
			defer server.Close()

			tool := newHTTPTool(30 * time.Second)
			out, err := runHTTPTool(t, tool, HTTPInput{URL: server.URL, Method: "GET"})
			if err != nil {
				t.Fatalf("expected no error for %d status, got: %v", tc.statusCode, err)
			}
			if out.StatusCode != tc.statusCode {
				t.Errorf("expected status %d, got %d", tc.statusCode, out.StatusCode)
			}
			if out.Body != "error body" {
				t.Errorf("expected body %q, got %q", "error body", out.Body)
			}
		})
	}
}

// Validates: TBOX-2.9
func TestHTTPTool_ConnectionError(t *testing.T) {
	// Start a server then close it immediately to get a connection error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	closedURL := server.URL
	server.Close()

	tool := newHTTPTool(30 * time.Second)
	raw, _ := json.Marshal(HTTPInput{URL: closedURL, Method: "GET"})
	_, err := tool.Run(context.Background(), raw)
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
	if !errors.Is(err, ErrConnection) {
		t.Errorf("expected ErrConnection, got: %v", err)
	}
}

// Validates: TBOX-2.10
func TestHTTPTool_ResponseBodyTruncation(t *testing.T) {
	// Generate a response larger than 5 MB.
	largeBody := strings.Repeat("x", maxResponseBody+1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(largeBody))
	}))
	defer server.Close()

	tool := newHTTPTool(30 * time.Second)
	out, err := runHTTPTool(t, tool, HTTPInput{URL: server.URL, Method: "GET"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Truncated {
		t.Error("expected Truncated to be true")
	}
	if len(out.Body) != maxResponseBody {
		t.Errorf("expected body length %d, got %d", maxResponseBody, len(out.Body))
	}
}

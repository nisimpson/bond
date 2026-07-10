package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
	"github.com/nisimpson/bond/runtime"
)

func TestHTTPHandler_Invocations_Success(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hello world"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})

	body := strings.NewReader(`{"prompt":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/invocations", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["response"] != "hello world" {
		t.Fatalf("expected 'hello world', got %q", result["response"])
	}
	if result["status"] != "success" {
		t.Fatalf("expected 'success', got %q", result["status"])
	}
}

func TestHTTPHandler_Invocations_InvalidBody(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hello"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})

	req := httptest.NewRequest(http.MethodPost, "/invocations", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHTTPHandler_Invocations_AgentError(t *testing.T) {
	agent := &bondtest.Agent{
		Err: errors.New("model failure"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})

	body := strings.NewReader(`{"prompt":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/invocations", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHTTPHandler_Ping_Default(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["status"] != "Healthy" {
		t.Fatalf("expected 'Healthy', got %q", result["status"])
	}
}

func TestHTTPHandler_Ping_CustomHandler(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{
		Options: runtime.Options{
			Ping: func(_ context.Context) (runtime.HealthStatus, error) {
				return runtime.HealthyBusy, nil
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["status"] != "HealthyBusy" {
		t.Fatalf("expected 'HealthyBusy', got %q", result["status"])
	}
}

func TestHTTPHandler_Ping_Error(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{
		Options: runtime.Options{
			Ping: func(_ context.Context) (runtime.HealthStatus, error) {
				return "", errors.New("unhealthy")
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHTTPHandler_CustomPaths(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("custom"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{
		InvocationsPath: "/custom/invoke",
		PingPath:        "/custom/health",
	})

	// Test custom invocations path
	body := strings.NewReader(`{"prompt":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/custom/invoke", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for custom invoke path, got %d: %s", rec.Code, rec.Body.String())
	}

	// Test custom ping path
	req = httptest.NewRequest(http.MethodGet, "/custom/health", nil)
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for custom health path, got %d", rec.Code)
	}
}

func TestHTTPHandler_Port(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}

	h := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{
		Port: ":9090",
	})
	if h.Port() != ":9090" {
		t.Fatalf("expected ':9090', got %q", h.Port())
	}
}

func TestHTTPHandler_DefaultPort(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}

	h := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})
	if h.Port() != ":8080" {
		t.Fatalf("expected ':8080', got %q", h.Port())
	}
}

func TestHTTPHandler_SSE(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("chunk1", "chunk2"),
	}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})

	body := strings.NewReader(`{"prompt":"stream me"}`)
	req := httptest.NewRequest(http.MethodPost, "/invocations", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Fatalf("expected 'text/event-stream', got %q", contentType)
	}

	respBody, _ := io.ReadAll(rec.Body)
	respStr := string(respBody)

	if !strings.Contains(respStr, "chunk1") {
		t.Fatalf("expected response to contain 'chunk1', got %q", respStr)
	}
	if !strings.Contains(respStr, "chunk2") {
		t.Fatalf("expected response to contain 'chunk2', got %q", respStr)
	}
}

func TestHTTPHandler_Middleware(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hello"),
	}

	middlewareCalled := false
	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{
		Options: runtime.Options{
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					middlewareCalled = true
					next.ServeHTTP(w, r)
				})
			},
		},
	})

	body := strings.NewReader(`{"prompt":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/invocations", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !middlewareCalled {
		t.Fatal("expected middleware to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHTTPHandler_EchoAgent(t *testing.T) {
	agent := &bondtest.EchoAgent{}

	handler := runtime.NewHTTPHandler(agent, runtime.HTTPOptions{})

	body := strings.NewReader(`{"prompt":"echo this back"}`)
	req := httptest.NewRequest(http.MethodPost, "/invocations", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["response"] != "echo this back" {
		t.Fatalf("expected 'echo this back', got %q", result["response"])
	}
}

func TestHandlePing_NilPingHandler(t *testing.T) {
	opts := runtime.Options{}
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()

	runtime.HandlePing(opts, rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result["status"] != "Healthy" {
		t.Fatalf("expected 'Healthy', got %q", result["status"])
	}
}

func TestDefaultAgentCard(t *testing.T) {
	card := runtime.DefaultAgentCard()
	if card.Name != "bond-agent" {
		t.Fatalf("expected 'bond-agent', got %q", card.Name)
	}
	if card.Version != "1.0.0" {
		t.Fatalf("expected '1.0.0', got %q", card.Version)
	}
	if !card.Capabilities.Streaming {
		t.Fatal("expected streaming capability to be true")
	}
}

func TestNewBondExecutor(t *testing.T) {
	agent := &bondtest.Agent{
		Events: bondtest.TextEvents("hi"),
	}
	executor := runtime.NewBondExecutor(agent, bond.AgentOptions{})
	if executor == nil {
		t.Fatal("expected non-nil executor")
	}
}

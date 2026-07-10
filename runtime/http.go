package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/internal/must"
)

// HTTPOptions configures the HTTP handler.
type HTTPOptions struct {
	Options
	// Port is the address to serve on (e.g. ":8080").
	Port string
	// InvocationsPath is the endpoint path. Defaults to "/invocations".
	InvocationsPath string
	// PingPath is the health check endpoint path. Defaults to "/ping".
	PingPath string
}

// HTTPHandler serves a REST/SSE HTTP protocol.
type HTTPHandler struct {
	agent bond.Agent
	opts  HTTPOptions
	mux   *http.ServeMux
}

// NewHTTPHandler creates an HTTP handler.
func NewHTTPHandler(agent bond.Agent, opts HTTPOptions) *HTTPHandler {
	invPath := opts.InvocationsPath
	if invPath == "" {
		invPath = "/invocations"
	}
	pingPath := opts.PingPath
	if pingPath == "" {
		pingPath = "/ping"
	}
	port := opts.Port
	if port == "" {
		port = ":8080"
	}

	h := &HTTPHandler{
		agent: agent,
		opts:  opts,
		mux:   http.NewServeMux(),
	}

	var invHandler http.Handler = http.HandlerFunc(h.handleInvocations)
	if opts.Middleware != nil {
		invHandler = opts.Middleware(invHandler)
	}

	h.mux.Handle("POST "+invPath, invHandler)
	h.mux.HandleFunc("GET "+pingPath, func(w http.ResponseWriter, r *http.Request) {
		HandlePing(opts.Options, w, r)
	})

	h.opts.Port = port
	return h
}

// Port returns the configured port.
func (h *HTTPHandler) Port() string { return h.opts.Port }

// ServeHTTP implements http.Handler.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type invocationRequest struct {
	Prompt string `json:"prompt"`
}

func (h *HTTPHandler) handleInvocations(w http.ResponseWriter, r *http.Request) {
	var req invocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	messages := bond.TextPrompt(req.Prompt)

	if r.Header.Get("Accept") == "text/event-stream" {
		h.handleSSE(w, r.Context(), messages)
		return
	}

	resp, err := bond.Invoke(r.Context(), h.agent, messages, h.opts.AgentOptions)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"response": resp.Text,
		"status":   "success",
	})
}

func (h *HTTPHandler) handleSSE(w http.ResponseWriter, ctx context.Context, messages []bond.Message) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for event, err := range bond.Stream(ctx, h.agent, messages, h.opts.AgentOptions) {
		if err != nil {
			fmt.Fprintf(w, "data: %s\n\n", must.JSON(map[string]string{"error": err.Error()}))
			flusher.Flush()
			return
		}
		if event.Type == bond.StreamEventTextDelta {
			fmt.Fprintf(w, "data: %s\n\n", must.JSON(map[string]string{"event": event.TextDelta}))
			flusher.Flush()
		}
	}
}

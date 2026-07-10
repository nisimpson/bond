package runtime

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/nisimpson/bond"
)

// A2AOptions configures the A2A handler.
type A2AOptions struct {
	Options
	// Port is the address to serve on (e.g. ":9000"). Used by Port() method.
	Port string
	// PingPath is the health check endpoint path. Defaults to "/ping".
	PingPath string
}

// A2AHandler serves the A2A protocol (JSON-RPC 2.0).
type A2AHandler struct {
	mux  *http.ServeMux
	port string
}

// NewA2AHandler creates an A2A handler wrapping a bond.Agent.
func NewA2AHandler(agent bond.Agent, opts A2AOptions) *A2AHandler {
	executor := &bondExecutor{agent: agent, opts: opts.AgentOptions}
	return NewA2AHandlerFromExecutor(executor, opts)
}

// NewA2AHandlerFromExecutor creates an A2A handler from a custom executor.
func NewA2AHandlerFromExecutor(executor a2asrv.AgentExecutor, opts A2AOptions) *A2AHandler {
	handlerOpts := append([]a2asrv.RequestHandlerOption{}, opts.A2AHandlerOptions...)
	requestHandler := a2asrv.NewHandler(executor, handlerOpts...)

	jsonrpcHandler := a2asrv.NewJSONRPCHandler(requestHandler)

	var protocolHandler http.Handler = jsonrpcHandler
	if opts.Middleware != nil {
		protocolHandler = opts.Middleware(protocolHandler)
	}

	pingPath := opts.PingPath
	if pingPath == "" {
		pingPath = "/ping"
	}

	card := opts.Card
	if card == nil {
		card = DefaultAgentCard()
	}

	mux := http.NewServeMux()
	mux.Handle("POST /", protocolHandler)
	mux.HandleFunc("GET "+pingPath, func(w http.ResponseWriter, r *http.Request) {
		HandlePing(opts.Options, w, r)
	})
	mux.HandleFunc("GET /.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})

	port := opts.Port
	if port == "" {
		port = ":9000"
	}

	return &A2AHandler{mux: mux, port: port}
}

// Port returns the configured port.
func (h *A2AHandler) Port() string { return h.port }

// ServeHTTP implements http.Handler.
func (h *A2AHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// ---------------------------------------------------------------------------
// bondExecutor bridges bond.Agent to a2asrv.AgentExecutor
// ---------------------------------------------------------------------------

type bondExecutor struct {
	agent bond.Agent
	opts  bond.AgentOptions
}

func (e *bondExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		// Emit initial Task with "working" state — required by a2asrv as the first event.
		if !yield(&a2a.Task{
			ID:        execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
		}, nil) {
			return
		}

		messages := a2aMessageToBond(execCtx.Message)

		var textBuf strings.Builder
		for event, err := range bond.Stream(ctx, e.agent, messages, e.opts) {
			if err != nil {
				yield(nil, err)
				return
			}
			if event.Type == bond.StreamEventTextDelta && event.TextDelta != "" {
				textBuf.WriteString(event.TextDelta)
			}
		}

		// Emit completed Task with full artifacts.
		task := &a2a.Task{
			ID:        execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}
		if textBuf.Len() > 0 {
			task.Artifacts = []*a2a.Artifact{
				{
					ID:    "response",
					Name:  "agent_response",
					Parts: a2a.ContentParts{a2a.NewTextPart(textBuf.String())},
				},
			}
		}
		yield(task, nil)
	}
}

func (e *bondExecutor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(&a2a.TaskStatusUpdateEvent{
			TaskID:    execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCanceled},
		}, nil)
	}
}

func a2aMessageToBond(msg *a2a.Message) []bond.Message {
	if msg == nil {
		return nil
	}
	var blocks []bond.Block
	for _, part := range msg.Parts {
		if text, ok := part.Content.(a2a.Text); ok {
			blocks = append(blocks, &bond.TextBlock{Text: string(text)})
		}
	}
	role := bond.RoleUser
	if msg.Role == a2a.MessageRoleAgent {
		role = bond.RoleAssistant
	}
	return []bond.Message{{Role: role, Content: blocks}}
}

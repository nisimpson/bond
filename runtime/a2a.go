package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"

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
		// Requirement: 4.5 — yield Task{Working} before any artifact events.
		if !yield(&a2a.Task{
			ID:        execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
		}, nil) {
			return
		}

		messages := a2aMessageToBond(execCtx.Message)

		textArtifactID := a2a.ArtifactID(fmt.Sprintf("%s-text", execCtx.TaskID))
		var hasText bool
		var currentMediaMIME string
		var mediaIndex int
		// Track all active media artifact IDs for final lastChunk yields.
		var mediaArtifactIDs []a2a.ArtifactID

		for event, err := range bond.Stream(ctx, e.agent, messages, e.opts) {
			// Requirement: 4.6 — on error, yield Task{Failed} with error message.
			if err != nil {
				yield(&a2a.Task{
					ID:        execCtx.TaskID,
					ContextID: execCtx.ContextID,
					Status: a2a.TaskStatus{
						State:   a2a.TaskStateFailed,
						Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error())),
					},
				}, nil)
				return
			}

			switch event.Type {
			case bond.StreamEventTextDelta:
				if event.TextDelta == "" {
					continue
				}
				hasText = true
				// Requirement: 4.1 — text delta yields TaskArtifactUpdateEvent with lastChunk: false.
				if !yield(&a2a.TaskArtifactUpdateEvent{
					TaskID:    execCtx.TaskID,
					ContextID: execCtx.ContextID,
					Append:    true,
					Artifact: &a2a.Artifact{
						ID:    textArtifactID,
						Name:  "agent_response",
						Parts: a2a.ContentParts{a2a.NewTextPart(event.TextDelta)},
					},
					LastChunk: false,
				}, nil) {
					return
				}

			case bond.StreamEventMediaDelta:
				if event.MediaDelta == nil {
					continue
				}
				// Requirement: 4.2 — media delta yields TaskArtifactUpdateEvent with lastChunk: false.
				// Requirement: 4.3 — assign unique artifact ID per distinct media stream.
				// Note: a "distinct media stream" is identified by MIME type transitions.
				// If the same MIME type reappears after a different one (e.g., png → wav → png),
				// it gets a new artifact ID. Clients should not assume same-MIME means same-artifact.
				if event.MediaDelta.MIMEType != currentMediaMIME {
					// New distinct media stream detected.
					currentMediaMIME = event.MediaDelta.MIMEType
					mediaIndex++
					mediaArtifactIDs = append(mediaArtifactIDs, a2a.ArtifactID(fmt.Sprintf("%s-media-%d", execCtx.TaskID, mediaIndex)))
				}
				currentMediaArtifactID := mediaArtifactIDs[len(mediaArtifactIDs)-1]

				part := a2a.NewRawPart(event.MediaDelta.Data)
				part.MediaType = event.MediaDelta.MIMEType

				if !yield(&a2a.TaskArtifactUpdateEvent{
					TaskID:    execCtx.TaskID,
					ContextID: execCtx.ContextID,
					Append:    true,
					Artifact: &a2a.Artifact{
						ID:    currentMediaArtifactID,
						Parts: a2a.ContentParts{part},
					},
					LastChunk: false,
				}, nil) {
					return
				}
			}
		}

		// Requirement: 4.4 — on stream completion, yield final lastChunk: true for each active artifact.
		if hasText {
			if !yield(&a2a.TaskArtifactUpdateEvent{
				TaskID:    execCtx.TaskID,
				ContextID: execCtx.ContextID,
				Artifact: &a2a.Artifact{
					ID:    textArtifactID,
					Name:  "agent_response",
					Parts: a2a.ContentParts{},
				},
				LastChunk: true,
			}, nil) {
				return
			}
		}
		for _, mediaID := range mediaArtifactIDs {
			if !yield(&a2a.TaskArtifactUpdateEvent{
				TaskID:    execCtx.TaskID,
				ContextID: execCtx.ContextID,
				Artifact: &a2a.Artifact{
					ID:    mediaID,
					Parts: a2a.ContentParts{},
				},
				LastChunk: true,
			}, nil) {
				return
			}
		}

		// Requirement: 4.7 — on empty stream, yield Task{Completed} with empty artifacts.
		// Requirement: 4.4 — yield Task{Completed} after final artifact events.
		yield(&a2a.Task{
			ID:        execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}, nil)
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

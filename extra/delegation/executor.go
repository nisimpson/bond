package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/internal/must"
)

// AgentFactory creates a bond.Agent configured with the given delegation
// skills. The returned AgentOptions should include a delegation Plugin
// wired with the provided Requester so proxy tools can delegate back to the
// client. Called once per context when skills are discovered.
type AgentFactory func(ctx context.Context, skills []Skill, requester Requester) (bond.Agent, bond.AgentOptions)

// ExecutorOptions configures the Executor.
type ExecutorOptions struct {
	// Factory creates the agent once skills are known. The Requester argument
	// is managed by the executor — pass it into delegation.NewPlugin so proxy
	// tools can delegate back to the client transparently.
	Factory AgentFactory
	// NegotiationMessage is the text sent to the client when skills are not
	// provided. Defaults to a standard request if empty.
	NegotiationMessage string
}

// Executor implements a2asrv.AgentExecutor with lazy agent instantiation
// and channel-based delegation support.
//
// On the first message, if no skills are present, it responds with "input
// required" asking the client to advertise skills. Once skills arrive, it
// builds the agent with a channel-based Requester. When a proxy tool fires,
// the executor surfaces the delegation request as an A2A "input required"
// event and suspends. When the client responds (next Execute call), the
// executor feeds the response back through the channel, unblocking the tool.
type Executor struct {
	opts     ExecutorOptions
	mu       sync.RWMutex
	sessions map[string]*session // keyed by ContextID
}

// session holds the state for a single delegation context.
type session struct {
	agent  bond.Agent
	opts   bond.AgentOptions
	bridge *channelRequester
	// pending holds queued events from the agent stream that arrived before
	// a delegation request. On resume, the executor replays them.
	pendingEvents []a2a.Event
}

var defaultNegotiationMsg = map[string]any{
	"type":    "delegation:skills_required",
	"message": "Please provide your available tools/skills in message metadata under the key 'delegation:skills'.",
	"example": map[string]any{
		"metadata": map[string]any{
			"delegation:skills": []map[string]any{
				{
					"name":        "web_fetch",
					"description": "fetches html from the web",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url": map[string]any{
								"type":        "string",
								"description": "The URL to fetch",
							},
						},
						"required": []string{"url"},
					},
				},
			},
		},
	},
}

// NewExecutor creates an executor that negotiates skills and manages
// delegation channels automatically.
func NewExecutor(opts ExecutorOptions) *Executor {
	if opts.NegotiationMessage == "" {
		msg := must.Return(json.Marshal(defaultNegotiationMsg))
		opts.NegotiationMessage = string(msg)
	}
	return &Executor{
		opts:     opts,
		sessions: make(map[string]*session),
	}
}

// Execute implements a2asrv.AgentExecutor.
func (d *Executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	// Check if we have an existing session for this context.
	d.mu.RLock()
	sess, hasSession := d.sessions[execCtx.ContextID]
	d.mu.RUnlock()

	if hasSession {
		if sess.bridge.isPending() {
			// Client is responding to a delegation request — feed it back.
			return d.resumeWithResponse(ctx, execCtx, sess)
		}
		// Existing session, no pending delegation — run with new message.
		return d.runAgent(ctx, execCtx, sess)
	}

	if execCtx.StoredTask != nil && execCtx.StoredTask.Status.State == a2a.TaskStateInputRequired {
		// Client is responding to an "input required" response from us; extract skills.
		skills, _ := extractSkills(execCtx.Message)
		if skills != nil {
			// Pass skills down to the agent and continue execution.
			return d.startAgent(ctx, execCtx, skills)
		}
		// No skills to be found -- request skills again from the client.
		return d.requestSkills(execCtx)
	}

	// First message — check for skills upfront.
	skills, _ := extractSkills(execCtx.Message)
	if skills != nil {
		// Pass skills down to the agent and continue execution.
		return d.startAgent(ctx, execCtx, skills)
	}

	// Otherwise, request skills.
	return d.requestSkills(execCtx)
}

// Cancel implements a2asrv.AgentExecutor.
func (d *Executor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	// Clean up the session.
	d.mu.Lock()
	if sess, ok := d.sessions[execCtx.ContextID]; ok {
		sess.bridge.cancel()
		delete(d.sessions, execCtx.ContextID)
	}
	d.mu.Unlock()

	return func(yield func(a2a.Event, error) bool) {
		yield(&a2a.TaskStatusUpdateEvent{
			TaskID:    execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCanceled},
		}, nil)
	}
}

// requestSkills emits an "input required" event asking the client to provide
// their skills/capabilities.
func (d *Executor) requestSkills(execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(&a2a.TaskStatusUpdateEvent{
			TaskID:    execCtx.TaskID,
			ContextID: execCtx.ContextID,
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateInputRequired,
				Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(d.opts.NegotiationMessage)),
			},
		}, nil)
	}
}

// startAgent creates the session, starts the agent in a goroutine, and
// returns events — pausing if a delegation request occurs.
func (d *Executor) startAgent(ctx context.Context, execCtx *a2asrv.ExecutorContext, skills []Skill) iter.Seq2[a2a.Event, error] {
	bridge := newChannelRequester()

	agent, agentOpts := d.opts.Factory(ctx, skills, bridge)

	sess := &session{
		agent:  agent,
		opts:   agentOpts,
		bridge: bridge,
	}

	d.mu.Lock()
	d.sessions[execCtx.ContextID] = sess
	d.mu.Unlock()

	return d.runAgent(ctx, execCtx, sess)
}

// runAgent runs bond.Stream in a goroutine and yields events. If a proxy tool
// triggers a delegation request, it yields "input required" and returns (the
// iter ends). The agent goroutine stays blocked on the channel.
func (d *Executor) runAgent(ctx context.Context, execCtx *a2asrv.ExecutorContext, sess *session) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		messages := a2aMessageToBondMessages(execCtx.Message)

		// Channel for the agent goroutine to send events/completion.
		type streamResult struct {
			event *bond.StreamEvent
			err   error
			done  bool
		}
		results := make(chan streamResult, 1)

		// Run bond.Stream in a goroutine so we can multiplex between
		// stream events and delegation requests.
		go func() {
			for event, err := range bond.Stream(ctx, sess.agent, messages, sess.opts) {
				if err != nil {
					results <- streamResult{err: err}
					return
				}
				e := event
				results <- streamResult{event: &e}
			}
			results <- streamResult{done: true}
		}()

		var textBuf strings.Builder

		for {
			select {
			case req := <-sess.bridge.requests:
				// A proxy tool is requesting delegation.
				// Emit "input required" with the tool call info.
				payload, _ := json.Marshal(delegationRequest{
					Type:  delegationRequestType,
					Tool:  req.toolName,
					Input: req.input,
				})
				if !yield(&a2a.TaskStatusUpdateEvent{
					TaskID:    execCtx.TaskID,
					ContextID: execCtx.ContextID,
					Status: a2a.TaskStatus{
						State:   a2a.TaskStateInputRequired,
						Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(string(payload))),
					},
				}, nil) {
					return
				}
				// Return — the iter ends. Agent goroutine stays blocked on
				// bridge.responses. Next Execute call will resume it.
				return

			case res := <-results:
				if res.err != nil {
					yield(nil, res.err)
					return
				}
				if res.done {
					// Agent finished — emit artifact and completion.
					if textBuf.Len() > 0 {
						if !yield(&a2a.TaskArtifactUpdateEvent{
							TaskID:    execCtx.TaskID,
							ContextID: execCtx.ContextID,
							Artifact: &a2a.Artifact{
								ID:    "response",
								Name:  "agent_response",
								Parts: a2a.ContentParts{a2a.NewTextPart(textBuf.String())},
							},
							LastChunk: true,
						}, nil) {
							return
						}
					}
					yield(&a2a.TaskStatusUpdateEvent{
						TaskID:    execCtx.TaskID,
						ContextID: execCtx.ContextID,
						Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
					}, nil)

					// Clean up session.
					d.mu.Lock()
					delete(d.sessions, execCtx.ContextID)
					d.mu.Unlock()
					return
				}

				// Regular stream event — accumulate text.
				if res.event.Type == bond.StreamEventTextDelta {
					textBuf.WriteString(res.event.TextDelta)
				}
			}
		}
	}
}

// resumeWithResponse feeds the client's response back to the blocked proxy
// tool and continues yielding events from the agent.
func (d *Executor) resumeWithResponse(ctx context.Context, execCtx *a2asrv.ExecutorContext, sess *session) iter.Seq2[a2a.Event, error] {
	// Extract response blocks from the client's message.
	var blocks []bond.Block
	if execCtx.Message != nil {
		for _, part := range execCtx.Message.Parts {
			if text, ok := part.Content.(a2a.Text); ok {
				blocks = append(blocks, &bond.TextBlock{Text: string(text)})
			}
		}
	}

	// Feed the response back — this unblocks the proxy tool.
	sess.bridge.respond(blocks)

	// Continue consuming agent events (reuse the same runAgent logic,
	// but the agent is already running — we just resume reading from it).
	// Since the goroutine is still running, we re-enter the event loop.
	return d.runAgent(ctx, execCtx, sess)
}

// ---------------------------------------------------------------------------
// channelRequester — bridges proxy tools to the executor
// ---------------------------------------------------------------------------

type delegationReq struct {
	toolName string
	input    json.RawMessage
}

// channelRequester implements Requester using channels. The proxy tool writes
// a request, blocks on the response. The executor reads the request, emits
// "input required", and later writes the response when the client replies.
type channelRequester struct {
	requests  chan delegationReq
	responses chan []bond.Block
	canceled  chan struct{}
	once      sync.Once
}

func newChannelRequester() *channelRequester {
	return &channelRequester{
		requests:  make(chan delegationReq, 1),
		responses: make(chan []bond.Block, 1),
		canceled:  make(chan struct{}),
	}
}

// RequestInput implements Requester. Called by the proxy tool — blocks until
// the executor feeds back the client's response.
func (c *channelRequester) RequestInput(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error) {
	// Send the request to the executor.
	select {
	case c.requests <- delegationReq{toolName: toolName, input: input}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.canceled:
		return nil, fmt.Errorf("delegation canceled")
	}

	// Wait for the response.
	select {
	case blocks := <-c.responses:
		return blocks, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.canceled:
		return nil, fmt.Errorf("delegation canceled")
	}
}

// respond sends the client's response to the blocked proxy tool.
func (c *channelRequester) respond(blocks []bond.Block) {
	c.responses <- blocks
}

// isPending returns true if there's a delegation request waiting.
func (c *channelRequester) isPending() bool {
	return len(c.requests) > 0 || len(c.responses) == 0
}

// cancel unblocks any waiting proxy tool.
func (c *channelRequester) cancel() {
	c.once.Do(func() { close(c.canceled) })
}

// a2aMessageToBondMessages converts an A2A message to bond conversation format.
func a2aMessageToBondMessages(msg *a2a.Message) []bond.Message {
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

package session

import (
	"context"
	"log/slog"
	"sync"

	"github.com/nisimpson/bond"
)

// Requirement: CONV-9.1, CONV-9.2 — session plugin construction

// SessionIDResolver extracts a session ID from the context.
// This allows the caller to control session identification (e.g., from HTTP headers,
// A2A task IDs, or explicit configuration).
type SessionIDResolver func(ctx context.Context) (string, error)

// SessionPluginOptions configures the SessionPlugin.
type SessionPluginOptions struct {
	// Store is the backing SessionStore.
	Store SessionStore
	// ResolveID extracts the session ID from the context.
	ResolveID SessionIDResolver
}

// SessionPlugin automatically loads session history before streaming and
// saves conversation turns as they are appended.
type SessionPlugin struct {
	opts SessionPluginOptions

	// mu protects accumulated messages across hooks.
	mu          sync.Mutex
	accumulated []bond.Message
}

// NewSessionPlugin creates a SessionPlugin with the given options.
func NewSessionPlugin(opts SessionPluginOptions) *SessionPlugin {
	return &SessionPlugin{opts: opts}
}

// Name identifies the plugin (used for logging/debugging).
func (p *SessionPlugin) Name() string { return "session" }

// Tools returns nil — the session plugin contributes no tools.
func (p *SessionPlugin) Tools() []bond.Tool { return nil }

// Init registers hooks on the provided registry.
// Requirement: CONV-9.3, CONV-9.5 — load session history before stream
// Requirement: CONV-9.4, CONV-9.6 — save on message append
func (p *SessionPlugin) Init(registry *bond.HookRegistry) {
	// BeforeStreamHook (gate): resolve session ID, load history, prepend to messages.
	bond.OnBefore(registry, func(ctx context.Context, hook *bond.BeforeStreamHook) error {
		sessionID, err := p.opts.ResolveID(ctx)
		if err != nil {
			return err
		}

		loaded, err := p.opts.Store.Load(ctx, sessionID)
		if err != nil {
			return err
		}

		if len(loaded) > 0 {
			hook.Messages = append(loaded, hook.Messages...)
		}

		// Initialize accumulated state: loaded history + original messages.
		p.mu.Lock()
		p.accumulated = make([]bond.Message, len(hook.Messages))
		copy(p.accumulated, hook.Messages)
		p.mu.Unlock()

		return nil
	})

	// AfterMessageAppendedHook (observer): resolve session ID, append message, save.
	bond.OnAfter(registry, func(ctx context.Context, hook *bond.AfterMessageAppendedHook) {
		sessionID, err := p.opts.ResolveID(ctx)
		if err != nil {
			slog.Error("session: failed to resolve session ID for save", "error", err)
			return
		}

		// Append the new message to accumulated history.
		p.mu.Lock()
		p.accumulated = append(p.accumulated, hook.Message)
		toSave := make([]bond.Message, len(p.accumulated))
		copy(toSave, p.accumulated)
		p.mu.Unlock()

		if err := p.opts.Store.Save(ctx, sessionID, toSave); err != nil {
			slog.Error("session: failed to save session", "session_id", sessionID, "error", err)
		}
	})
}

// Verify interface compliance.
var _ bond.Plugin = (*SessionPlugin)(nil)

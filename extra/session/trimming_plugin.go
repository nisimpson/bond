package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nisimpson/bond"
)

// Requirement: CONV-4.1, CONV-4.2, CONV-4.6 — TrimmingPlugin construction and package location

// TrimmingPluginOptions configures the TrimmingPlugin.
type TrimmingPluginOptions struct {
	// Manager is the ConversationManager to invoke on each hook.
	Manager ConversationManager
	// AutoRecover enables automatic retry on ErrContextOverflow.
	AutoRecover bool
	// MaxRetries is the maximum number of trim-and-retry attempts.
	// Defaults to 1 if AutoRecover is true and MaxRetries is 0.
	MaxRetries int
}

// TrimmingPlugin registers a BeforeModelInvokeHook that trims conversation
// history before each model invocation. It also provides auto-recovery from
// context overflow errors when configured.
type TrimmingPlugin struct {
	opts TrimmingPluginOptions

	// mu protects retryCount across hook invocations.
	mu         sync.Mutex
	retryCount int
}

// NewTrimmingPlugin creates a TrimmingPlugin with the given options.
// If AutoRecover is true and MaxRetries is 0, MaxRetries defaults to 1.
func NewTrimmingPlugin(opts TrimmingPluginOptions) *TrimmingPlugin {
	if opts.AutoRecover && opts.MaxRetries == 0 {
		opts.MaxRetries = 1
	}
	return &TrimmingPlugin{opts: opts}
}

// Name identifies the plugin (used for logging/debugging).
func (p *TrimmingPlugin) Name() string { return "trimming" }

// Tools returns nil — the trimming plugin contributes no tools.
func (p *TrimmingPlugin) Tools() []bond.Tool { return nil }

// Init registers hooks on the provided registry.
// Requirement: CONV-4.2, CONV-4.3, CONV-4.4, CONV-4.5 — BeforeModelInvokeHook registration
func (p *TrimmingPlugin) Init(registry *bond.HookRegistry) {
	bond.OnBefore(registry, bond.BeforeHookFunc[*bond.BeforeModelInvokeHook](
		func(ctx context.Context, hook *bond.BeforeModelInvokeHook) error {
			trimmed, err := p.opts.Manager.Trim(ctx, hook.Messages)
			if err != nil {
				return err
			}
			hook.Messages = trimmed
			return nil
		},
	))
}

// Recover attempts to recover from a context overflow error by trimming
// messages and tracking retry attempts. It should be called by the agent loop
// or client code when a provider returns ErrContextOverflow.
//
// Requirement: CONV-5.1, CONV-5.2, CONV-5.3, CONV-5.4, CONV-5.5
//
// Returns the trimmed messages on success. Returns an error if:
//   - auto-recovery is not enabled
//   - the error is not a context overflow error
//   - max retries have been exhausted
//   - the trim operation itself fails
func (p *TrimmingPlugin) Recover(ctx context.Context, err error, messages []bond.Message) ([]bond.Message, error) {
	if !p.opts.AutoRecover {
		return nil, fmt.Errorf("session: auto-recovery is not enabled: %w", err)
	}

	if !errors.Is(err, bond.ErrContextOverflow) {
		return nil, fmt.Errorf("session: not a context overflow error: %w", err)
	}

	p.mu.Lock()
	if p.retryCount >= p.opts.MaxRetries {
		p.mu.Unlock()
		return nil, fmt.Errorf("session: max retries (%d) exhausted: %w", p.opts.MaxRetries, err)
	}
	p.retryCount++
	p.mu.Unlock()

	trimmed, trimErr := p.opts.Manager.Trim(ctx, messages)
	if trimErr != nil {
		return nil, fmt.Errorf("session: trim during recovery failed: %w", trimErr)
	}

	return trimmed, nil
}

// ResetRetries resets the retry counter, allowing auto-recovery attempts again.
// This should be called at the start of a new stream or conversation turn.
func (p *TrimmingPlugin) ResetRetries() {
	p.mu.Lock()
	p.retryCount = 0
	p.mu.Unlock()
}

// Verify interface compliance.
var _ bond.Plugin = (*TrimmingPlugin)(nil)

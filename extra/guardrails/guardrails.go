package guardrails

import (
	"context"
	"strings"

	"github.com/nisimpson/bond"
)

// Direction indicates whether content is being sent to or received from the model.
type Direction string

const (
	// DirectionInput is content being sent to the model (user messages).
	DirectionInput Direction = "input"
	// DirectionOutput is content received from the model (assistant response).
	DirectionOutput Direction = "output"
)

// ActionType determines how filtered content should be handled.
type ActionType string

const (
	// ActionAllow permits the content to pass through unchanged.
	ActionAllow ActionType = "allow"
	// ActionBlock prevents the content from reaching the model (input) or
	// being returned to the caller (output).
	ActionBlock ActionType = "block"
	// ActionRedact replaces the matched content with a placeholder.
	ActionRedact ActionType = "redact"
	// ActionWarn allows the content but flags it for attention.
	ActionWarn ActionType = "warn"
	// ActionLog allows the content but records the match.
	ActionLog ActionType = "log"
)

// FilterResult is the outcome of applying a content filter.
type FilterResult struct {
	// Action is the recommended action for the content.
	Action ActionType
	// Reason describes why the filter triggered.
	Reason string
	// FilterName identifies which filter produced this result.
	FilterName string
	// Redacted is the replacement text when Action == ActionRedact.
	Redacted string
	// Direction indicates whether this was input or output filtering.
	Direction Direction
}

// ContentFilter examines text content and returns a filter result.
type ContentFilter interface {
	// Name identifies this filter (for logging and result attribution).
	Name() string
	// Filter examines content and returns a result. Return ActionAllow
	// to indicate no issues found.
	Filter(ctx context.Context, content string, direction Direction) FilterResult
}

// PluginOptions configures the guardrails plugin.
type PluginOptions struct {
	// Filters is the ordered list of content filters to apply.
	// Filters are evaluated in order; the first non-Allow result wins.
	Filters []ContentFilter
	// OnBlock is called when content is blocked. Optional.
	OnBlock func(ctx context.Context, result FilterResult)
	// OnWarn is called when content triggers a warning. Optional.
	OnWarn func(ctx context.Context, result FilterResult)
	// OnLog is called when content triggers a log action. Optional.
	OnLog func(ctx context.Context, result FilterResult)
	// BlockMessage is the text returned to the model when input is blocked.
	// Defaults to "I'm unable to process this request."
	BlockMessage string
}

// Plugin is the guardrails plugin that filters agent input and output.
type Plugin struct {
	opts PluginOptions
}

// NewPlugin creates a guardrails plugin with the given options.
func NewPlugin(opts PluginOptions) *Plugin {
	if opts.BlockMessage == "" {
		opts.BlockMessage = "I'm unable to process this request."
	}
	return &Plugin{opts: opts}
}

// Name implements [bond.Plugin].
func (p *Plugin) Name() string { return "guardrails" }

// Tools implements [bond.Plugin]. Guardrails contributes no tools.
func (p *Plugin) Tools() []bond.Tool { return nil }

// Init implements [bond.Plugin]. Registers input and output filtering hooks.
func (p *Plugin) Init(registry *bond.HookRegistry) {
	// Input filtering: before model invocation, scan user messages.
	bond.OnBefore(registry, func(ctx context.Context, hook *bond.BeforeModelInvokeHook) error {
		content := extractText(hook.Messages, bond.RoleUser)
		if content == "" {
			return nil
		}

		result := p.evaluate(ctx, content, DirectionInput)
		switch result.Action {
		case ActionBlock:
			if p.opts.OnBlock != nil {
				p.opts.OnBlock(ctx, result)
			}
			return bond.ErrAbort
		case ActionWarn:
			if p.opts.OnWarn != nil {
				p.opts.OnWarn(ctx, result)
			}
		case ActionLog:
			if p.opts.OnLog != nil {
				p.opts.OnLog(ctx, result)
			}
		}
		return nil
	})

	// Output filtering: after model invocation, scan assistant response.
	bond.OnAfter(registry, func(ctx context.Context, hook *bond.AfterModelInvokeHook) {
		content := blocksToText(hook.Blocks)
		if content == "" {
			return
		}

		result := p.evaluate(ctx, content, DirectionOutput)
		switch result.Action {
		case ActionBlock:
			if p.opts.OnBlock != nil {
				p.opts.OnBlock(ctx, result)
			}
			// Replace output with block message.
			hook.Blocks = []bond.Block{&bond.TextBlock{Text: p.opts.BlockMessage}}
		case ActionRedact:
			// Replace output with redacted content.
			hook.Blocks = []bond.Block{&bond.TextBlock{Text: result.Redacted}}
		case ActionWarn:
			if p.opts.OnWarn != nil {
				p.opts.OnWarn(ctx, result)
			}
		case ActionLog:
			if p.opts.OnLog != nil {
				p.opts.OnLog(ctx, result)
			}
		}
	})
}

// evaluate runs all filters against the content and returns the first non-Allow result.
func (p *Plugin) evaluate(ctx context.Context, content string, dir Direction) FilterResult {
	for _, f := range p.opts.Filters {
		result := f.Filter(ctx, content, dir)
		result.FilterName = f.Name()
		result.Direction = dir
		if result.Action != ActionAllow {
			return result
		}
	}
	return FilterResult{Action: ActionAllow}
}

// extractText concatenates text blocks from the last message with the given role.
func extractText(messages []bond.Message, role bond.Role) string {
	// Scan from the end to find the most recent message with this role.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == role {
			return blocksToText(messages[i].Content)
		}
	}
	return ""
}

// blocksToText concatenates all text blocks into a single string.
func blocksToText(blocks []bond.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		if tb, ok := b.(*bond.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}

// Verify interface compliance.
var _ bond.Plugin = (*Plugin)(nil)

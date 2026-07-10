// Package bedrock provides a bond.Agent implementation backed by the
// Amazon Bedrock Converse streaming API.
package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/nisimpson/bond"
)

// Client defines the subset of the Bedrock Runtime client used by Agent.
// This allows mocking for tests.
type Client interface {
	ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// AgentOptions configures the Bedrock agent.
type AgentOptions struct {
	// ModelID is the Bedrock model identifier (e.g. "anthropic.claude-3-sonnet-20240229-v1:0").
	ModelID string
	// System is the system prompt sent with every request.
	System string
	// InferenceConfig sets optional inference parameters (temperature, max tokens, etc).
	InferenceConfig *types.InferenceConfiguration
}

// Agent implements bond.Agent using Amazon Bedrock's ConverseStream API.
type Agent struct {
	client Client
	opts   AgentOptions
}

// New creates a Bedrock-backed bond.Agent.
func New(client Client, opts AgentOptions) *Agent {
	return &Agent{client: client, opts: opts}
}

// Stream implements bond.Agent. It converts bond messages to Bedrock format,
// calls ConverseStream, and translates the event stream into bond StreamEvents.
func (a *Agent) Stream(ctx context.Context, messages []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		input := a.buildInput(ctx, messages)

		output, err := a.client.ConverseStream(ctx, input)
		if err != nil {
			yield(bond.StreamEvent{}, fmt.Errorf("bedrock: %w", err))
			return
		}

		stream := output.GetStream()
		defer stream.Close()

		if !yield(bond.StreamEvent{Type: bond.StreamEventStart}, nil) {
			return
		}

		// Track tool use blocks being assembled across deltas.
		var pendingToolUse *bond.ToolUseBlock
		var toolInputBuf string

		for event := range stream.Events() {
			switch e := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockStart:
				// Tool use starts here — capture name and ID.
				if start, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
					pendingToolUse = &bond.ToolUseBlock{
						ID:   aws.ToString(start.Value.ToolUseId),
						Name: aws.ToString(start.Value.Name),
					}
					toolInputBuf = ""
				}

			case *types.ConverseStreamOutputMemberContentBlockDelta:
				switch delta := e.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					if !yield(bond.StreamEvent{
						Type:      bond.StreamEventTextDelta,
						TextDelta: delta.Value,
					}, nil) {
						return
					}

				case *types.ContentBlockDeltaMemberToolUse:
					// Accumulate tool input JSON fragments.
					if delta.Value.Input != nil {
						toolInputBuf += aws.ToString(delta.Value.Input)
					}
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:
				// If we were building a tool use block, emit it now.
				if pendingToolUse != nil {
					pendingToolUse.Input = json.RawMessage(toolInputBuf)
					if !yield(bond.StreamEvent{
						Type:    bond.StreamEventToolUse,
						ToolUse: pendingToolUse,
					}, nil) {
						return
					}
					pendingToolUse = nil
					toolInputBuf = ""
				}

			case *types.ConverseStreamOutputMemberMessageStop:
				stopReason := mapStopReason(e.Value.StopReason)
				yield(bond.StreamEvent{
					Type:       bond.StreamEventStop,
					StopReason: stopReason,
				}, nil)
				return
			}
		}

		// If stream ended without a MessageStop (shouldn't happen), emit stop.
		yield(bond.StreamEvent{Type: bond.StreamEventStop, StopReason: bond.StopReasonEnd}, nil)
	}
}

// buildInput constructs the ConverseStreamInput from bond messages.
func (a *Agent) buildInput(ctx context.Context, messages []bond.Message) *bedrockruntime.ConverseStreamInput {
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(a.opts.ModelID),
		Messages:        toBedrockMessages(messages),
		InferenceConfig: a.opts.InferenceConfig,
	}

	if a.opts.System != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: a.opts.System},
		}
	}

	if tools := bond.ToolsFromContext(ctx); len(tools) > 0 {
		input.ToolConfig = toBedrockToolConfig(tools)
	}

	return input
}

// toBedrockMessages converts bond messages to Bedrock message format.
func toBedrockMessages(messages []bond.Message) []types.Message {
	out := make([]types.Message, 0, len(messages))
	for _, msg := range messages {
		bMsg := types.Message{
			Role:    toBedrockRole(msg.Role),
			Content: toBedrockContent(msg.Content),
		}
		out = append(out, bMsg)
	}
	return out
}

// toBedrockRole maps bond roles to Bedrock roles.
func toBedrockRole(role bond.Role) types.ConversationRole {
	switch role {
	case bond.RoleAssistant:
		return types.ConversationRoleAssistant
	default:
		return types.ConversationRoleUser
	}
}

// toBedrockContent converts bond blocks to Bedrock content blocks.
func toBedrockContent(blocks []bond.Block) []types.ContentBlock {
	out := make([]types.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch block := b.(type) {
		case *bond.TextBlock:
			out = append(out, &types.ContentBlockMemberText{Value: block.Text})
		case *bond.ToolUseBlock:
			out = append(out, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: aws.String(block.ID),
					Name:      aws.String(block.Name),
					Input:     toDocument(block.Input),
				},
			})
		case *bond.ToolResultBlock:
			content := make([]types.ToolResultContentBlock, 0, len(block.Content))
			for _, c := range block.Content {
				if tb, ok := c.(*bond.TextBlock); ok {
					content = append(content, &types.ToolResultContentBlockMemberText{Value: tb.Text})
				}
			}
			status := types.ToolResultStatusSuccess
			if block.IsError {
				status = types.ToolResultStatusError
			}
			out = append(out, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					ToolUseId: aws.String(block.ToolUseID),
					Content:   content,
					Status:    status,
				},
			})
		}
	}
	return out
}

// toBedrockToolConfig converts bond tools to Bedrock tool configuration.
func toBedrockToolConfig(tools []bond.Tool) *types.ToolConfiguration {
	bedrockTools := make([]types.Tool, 0, len(tools))
	for _, t := range tools {
		schema := toToolInputSchema(t.InputSchema())
		bedrockTools = append(bedrockTools, &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(t.Name()),
				Description: aws.String(t.Description()),
				InputSchema: schema,
			},
		})
	}
	return &types.ToolConfiguration{Tools: bedrockTools}
}

// toToolInputSchema marshals a json.Marshaler into a Bedrock ToolInputSchema.
func toToolInputSchema(schema json.Marshaler) types.ToolInputSchema {
	if schema == nil {
		return &types.ToolInputSchemaMemberJson{Value: toDocumentFromBytes([]byte(`{"type":"object"}`))}
	}
	data, err := schema.MarshalJSON()
	if err != nil {
		return &types.ToolInputSchemaMemberJson{Value: toDocumentFromBytes([]byte(`{"type":"object"}`))}
	}
	return &types.ToolInputSchemaMemberJson{Value: toDocumentFromBytes(data)}
}

// mapStopReason converts Bedrock stop reasons to bond stop reasons.
func mapStopReason(reason types.StopReason) bond.StopReason {
	switch reason {
	case types.StopReasonToolUse:
		return bond.StopReasonToolUse
	case types.StopReasonMaxTokens:
		return bond.StopReasonLength
	default:
		return bond.StopReasonEnd
	}
}

// Verify interface compliance.
var _ bond.Agent = (*Agent)(nil)

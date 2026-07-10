package bedrock_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/provider/bedrock"
)

// mockClient implements bedrock.Client for testing.
type mockClient struct {
	err error
}

func (m *mockClient) ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return nil, errors.New("no mock stream configured")
}

func TestNew(t *testing.T) {
	client := &mockClient{}
	agent := bedrock.New(client, bedrock.AgentOptions{
		ModelID: "test-model",
		System:  "You are helpful",
	})
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestAgent_ImplementsInterface(t *testing.T) {
	var _ bond.Agent = bedrock.New(&mockClient{}, bedrock.AgentOptions{})
}

func TestStream_ClientError(t *testing.T) {
	client := &mockClient{err: errors.New("connection failed")}
	agent := bedrock.New(client, bedrock.AgentOptions{ModelID: "test"})

	var gotErr error
	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hi")) {
		if err != nil {
			gotErr = err
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected error")
	}
	if gotErr.Error() != "bedrock: connection failed" {
		t.Fatalf("unexpected error: %v", gotErr)
	}
}

func TestStream_WithSystemPrompt(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{
		ModelID: "test",
		System:  "You are a spy",
	})

	// Just verify it doesn't panic — the error path exercises buildInput.
	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hi")) {
		if err != nil {
			break
		}
	}
}

func TestStream_WithInferenceConfig(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{
		ModelID: "test",
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(1000),
			Temperature: aws.Float32(0.7),
		},
	})

	for _, err := range agent.Stream(context.Background(), bond.TextPrompt("hi")) {
		if err != nil {
			break
		}
	}
}

func TestStream_WithTools(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{ModelID: "test"})

	tool := &fakeTool{name: "search", desc: "search the web"}

	// Use bond.Stream which injects tools into context.
	for _, err := range bond.Stream(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Tools: []bond.Tool{tool},
	}) {
		if err != nil {
			break
		}
	}
}

func TestStream_MultipleMessages(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{ModelID: "test"})

	messages := bond.Conversation("user msg", "assistant msg", "another user msg")
	for _, err := range agent.Stream(context.Background(), messages) {
		if err != nil {
			break
		}
	}
}

func TestStream_ToolResultMessages(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{ModelID: "test"})

	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hi"}}},
		{Role: bond.RoleAssistant, Content: []bond.Block{
			&bond.ToolUseBlock{ID: "1", Name: "search", Input: json.RawMessage(`{"q":"go"}`)},
		}},
		{Role: bond.RoleUser, Content: []bond.Block{
			&bond.ToolResultBlock{ToolUseID: "1", Content: []bond.Block{&bond.TextBlock{Text: "results"}}, IsError: false},
		}},
	}

	for _, err := range agent.Stream(context.Background(), messages) {
		if err != nil {
			break
		}
	}
}

func TestStream_ToolResultError(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{ModelID: "test"})

	messages := []bond.Message{
		{Role: bond.RoleUser, Content: []bond.Block{&bond.TextBlock{Text: "hi"}}},
		{Role: bond.RoleAssistant, Content: []bond.Block{
			&bond.ToolUseBlock{ID: "1", Name: "fail", Input: json.RawMessage(`{}`)},
		}},
		{Role: bond.RoleUser, Content: []bond.Block{
			&bond.ToolResultBlock{ToolUseID: "1", Content: []bond.Block{&bond.TextBlock{Text: "error occurred"}}, IsError: true},
		}},
	}

	for _, err := range agent.Stream(context.Background(), messages) {
		if err != nil {
			break
		}
	}
}

func TestStream_NilToolSchema(t *testing.T) {
	client := &mockClient{err: errors.New("expected")}
	agent := bedrock.New(client, bedrock.AgentOptions{ModelID: "test"})

	tool := &fakeTool{name: "no_schema", desc: "no schema", nilSchema: true}

	for _, err := range bond.Stream(context.Background(), agent, bond.TextPrompt("hi"), bond.AgentOptions{
		Tools: []bond.Tool{tool},
	}) {
		if err != nil {
			break
		}
	}
}

// --- helpers ---

type fakeTool struct {
	name      string
	desc      string
	nilSchema bool
}

func (t *fakeTool) Name() string        { return t.name }
func (t *fakeTool) Description() string { return t.desc }
func (t *fakeTool) InputSchema() json.Marshaler {
	if t.nilSchema {
		return nil
	}
	return json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
}
func (t *fakeTool) Run(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
	return []bond.Block{&bond.TextBlock{Text: "ok"}}, nil
}

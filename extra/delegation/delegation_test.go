package delegation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/bondtest"
)

// --- Unit Tests ---

func TestSkillsFromTools(t *testing.T) {
	tools := []bond.Tool{
		&bondtest.FakeTool{ToolName: "search", ToolDesc: "Search the web"},
		&bondtest.FakeTool{ToolName: "calc", ToolDesc: "Calculator"},
	}

	skills := SkillsFromTools(tools)

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	if skills[0].Name != "search" {
		t.Errorf("expected skill name 'search', got %q", skills[0].Name)
	}
	if skills[1].Description != "Calculator" {
		t.Errorf("expected description 'Calculator', got %q", skills[1].Description)
	}
}

func TestAttachAndExtractSkills(t *testing.T) {
	skills := []Skill{
		{Name: "search", Description: "Search the web", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)},
		{Name: "calc", Description: "Calculator"},
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	if err := attachSkills(msg, skills); err != nil {
		t.Fatalf("AttachSkills: %v", err)
	}

	extracted, err := extractSkills(msg)
	if err != nil {
		t.Fatalf("ExtractSkills: %v", err)
	}

	if len(extracted) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(extracted))
	}
	if extracted[0].Name != "search" {
		t.Errorf("expected name 'search', got %q", extracted[0].Name)
	}
	if extracted[1].Name != "calc" {
		t.Errorf("expected name 'calc', got %q", extracted[1].Name)
	}

	// Verify schema round-trips
	schema, err := extracted[0].InputSchema.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(schema) != `{"type":"object","properties":{"q":{"type":"string"}}}` {
		t.Errorf("schema mismatch: %s", schema)
	}
}

func TestExtractSkills_NilMessage(t *testing.T) {
	skills, err := extractSkills(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skills != nil {
		t.Errorf("expected nil skills, got %v", skills)
	}
}

func TestExtractSkills_NoMetadata(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	skills, err := extractSkills(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skills != nil {
		t.Errorf("expected nil skills, got %v", skills)
	}
}

func TestFulfiller_Execute(t *testing.T) {
	tool := &bondtest.FakeTool{
		ToolName: "search",
		ToolDesc: "Search",
		RunFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			return []bond.Block{&bond.TextBlock{Text: "result for: " + string(input)}}, nil
		},
	}

	fulfiller := NewFulfiller(tool)

	blocks, err := fulfiller.Execute(context.Background(), "search", json.RawMessage(`{"q":"Go"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tb := blocks[0].(*bond.TextBlock)
	if tb.Text != `result for: {"q":"Go"}` {
		t.Errorf("unexpected result: %q", tb.Text)
	}
}

func TestFulfiller_UnknownTool(t *testing.T) {
	fulfiller := NewFulfiller()
	_, err := fulfiller.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestPlugin_ProxyToolRun(t *testing.T) {
	// Mock requester that captures the request and returns a canned response.
	requester := &mockRequester{
		response: []bond.Block{&bond.TextBlock{Text: "delegated result"}},
	}

	plugin := NewPlugin(Options{
		Requester: requester,
		Skills: []Skill{
			{Name: "search", Description: "Search the web"},
		},
	})

	tools := plugin.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool.Name() != "search" {
		t.Errorf("expected tool name 'search', got %q", tool.Name())
	}

	blocks, err := tool.Run(context.Background(), json.RawMessage(`{"q":"test"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].(*bond.TextBlock).Text != "delegated result" {
		t.Errorf("unexpected result: %q", blocks[0].(*bond.TextBlock).Text)
	}

	// Verify the requester received the right call.
	if requester.lastToolName != "search" {
		t.Errorf("expected tool name 'search', got %q", requester.lastToolName)
	}
	if string(requester.lastInput) != `{"q":"test"}` {
		t.Errorf("unexpected input: %s", requester.lastInput)
	}
}

// --- Integration Test ---

// TestIntegration_FullDelegationFlow simulates the complete delegation flow
// between a caller and target using in-process channels.
func TestIntegration_FullDelegationFlow(t *testing.T) {
	ctx := context.Background()

	// Caller's tool: a "search" tool that returns static results.
	searchTool := &bondtest.FakeTool{
		ToolName: "search",
		ToolDesc: "Search the web",
		RunFn: func(ctx context.Context, input json.RawMessage) ([]bond.Block, error) {
			var params struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(input, &params)
			return []bond.Block{&bond.TextBlock{Text: "Results for: " + params.Query}}, nil
		},
	}

	// Caller side: build fulfiller and extract skills.
	fulfiller := NewFulfiller(searchTool)
	skills := SkillsFromTools([]bond.Tool{searchTool})

	// Simulate: caller attaches skills to a message.
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("write about Go"))
	if err := attachSkills(msg, skills); err != nil {
		t.Fatalf("AttachSkills: %v", err)
	}

	// Target side: extract skills from received message.
	receivedSkills, err := extractSkills(msg)
	if err != nil {
		t.Fatalf("ExtractSkills: %v", err)
	}

	// Target creates a channel-based requester that calls back to the fulfiller.
	requester := &inProcessRequester{fulfiller: fulfiller}

	// Target creates delegation plugin with received skills.
	plugin := NewPlugin(Options{
		Requester: requester,
		Skills:    receivedSkills,
	})

	// Target's proxy tool should work end-to-end.
	tools := plugin.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 proxy tool, got %d", len(tools))
	}

	// Simulate: target model calls "search" proxy tool.
	result, err := tools[0].Run(ctx, json.RawMessage(`{"query":"Go programming"}`))
	if err != nil {
		t.Fatalf("proxy tool Run: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 result block, got %d", len(result))
	}

	text := result[0].(*bond.TextBlock).Text
	expected := "Results for: Go programming"
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

// --- Test helpers ---

type mockRequester struct {
	lastToolName string
	lastInput    json.RawMessage
	response     []bond.Block
	err          error
}

func (r *mockRequester) RequestInput(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error) {
	r.lastToolName = toolName
	r.lastInput = input
	return r.response, r.err
}

// inProcessRequester simulates A2A communication in-process by calling the
// fulfiller directly. This is what the delegation round-trip looks like
// without network transport.
type inProcessRequester struct {
	fulfiller *Fulfiller
}

func (r *inProcessRequester) RequestInput(ctx context.Context, toolName string, input json.RawMessage) ([]bond.Block, error) {
	return r.fulfiller.Execute(ctx, toolName, input)
}

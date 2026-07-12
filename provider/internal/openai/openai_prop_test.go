package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	bond "github.com/nisimpson/bond"
)

// --- Generators ---

// randPropString generates a random non-empty printable ASCII string.
func randPropString(rng *rand.Rand, maxLen int) string {
	length := rng.Intn(maxLen) + 1
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = byte(rng.Intn(94) + 32) // printable ASCII
	}
	return string(buf)
}

// randPropAlpha generates a random alphabetic string suitable for identifiers.
func randPropAlpha(rng *rand.Rand, maxLen int) string {
	length := rng.Intn(maxLen) + 1
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = byte(rng.Intn(26) + 'a')
	}
	return string(buf)
}

// messageInput is a custom generator that produces a random bond.Message slice
// containing TextBlock, ToolUseBlock, and ToolResultBlock entries.
type messageInput struct {
	Messages []bond.Message
}

func (messageInput) Generate(rng *rand.Rand, size int) reflect.Value {
	numMessages := rng.Intn(5) + 1
	messages := make([]bond.Message, numMessages)

	for i := range messages {
		role := bond.RoleUser
		if rng.Intn(2) == 0 {
			role = bond.RoleAssistant
		}

		var blocks []bond.Block
		numBlocks := rng.Intn(3) + 1
		for j := 0; j < numBlocks; j++ {
			switch rng.Intn(3) {
			case 0:
				blocks = append(blocks, &bond.TextBlock{Text: randPropString(rng, 30)})
			case 1:
				args := fmt.Sprintf(`{"key":"%s"}`, randPropAlpha(rng, 8))
				blocks = append(blocks, &bond.ToolUseBlock{
					ID:    "call_" + randPropAlpha(rng, 8),
					Name:  randPropAlpha(rng, 12),
					Input: json.RawMessage(args),
				})
			case 2:
				blocks = append(blocks, &bond.ToolResultBlock{
					ToolUseID: "call_" + randPropAlpha(rng, 8),
					Content:   []bond.Block{&bond.TextBlock{Text: randPropString(rng, 20)}},
				})
			}
		}

		messages[i] = bond.Message{Role: role, Content: blocks}
	}

	return reflect.ValueOf(messageInput{Messages: messages})
}

// systemPromptInput generates a non-empty system prompt string and a message sequence.
type systemPromptInput struct {
	SystemPrompt string
	Messages     []bond.Message
}

func (systemPromptInput) Generate(rng *rand.Rand, size int) reflect.Value {
	prompt := randPropString(rng, 50)

	numMessages := rng.Intn(4) + 1
	messages := make([]bond.Message, numMessages)
	for i := range messages {
		role := bond.RoleUser
		if rng.Intn(2) == 0 {
			role = bond.RoleAssistant
		}
		messages[i] = bond.Message{
			Role:    role,
			Content: []bond.Block{&bond.TextBlock{Text: randPropString(rng, 20)}},
		}
	}

	return reflect.ValueOf(systemPromptInput{
		SystemPrompt: prompt,
		Messages:     messages,
	})
}

// toolInput generates a random mock bond.Tool.
type toolInput struct {
	ToolName   string
	ToolDesc   string
	SchemaJSON string
}

func (toolInput) Generate(rng *rand.Rand, size int) reflect.Value {
	name := randPropAlpha(rng, 15)
	desc := randPropString(rng, 40)
	schema := fmt.Sprintf(`{"type":"object","properties":{"%s":{"type":"string"}}}`, randPropAlpha(rng, 8))

	return reflect.ValueOf(toolInput{
		ToolName:   name,
		ToolDesc:   desc,
		SchemaJSON: schema,
	})
}

// propMockTool implements bond.Tool for property testing.
type propMockTool struct {
	name   string
	desc   string
	schema json.RawMessage
}

func (m *propMockTool) Name() string                { return m.name }
func (m *propMockTool) Description() string         { return m.desc }
func (m *propMockTool) InputSchema() json.Marshaler { return m.schema }
func (m *propMockTool) Run(_ context.Context, _ json.RawMessage) ([]bond.Block, error) {
	return nil, nil
}

// sseChunksInput generates random SSE chunks with text content deltas.
type sseChunksInput struct {
	Contents []string // non-empty content strings per chunk
}

func (sseChunksInput) Generate(rng *rand.Rand, size int) reflect.Value {
	numChunks := rng.Intn(5) + 1
	contents := make([]string, numChunks)
	for i := range contents {
		contents[i] = randPropString(rng, 30)
	}
	return reflect.ValueOf(sseChunksInput{Contents: contents})
}

// toolCallDeltasInput generates random tool call delta sequences for accumulation testing.
type toolCallDeltasInput struct {
	NumTools  int
	ToolIDs   []string
	NameParts [][]string // per-tool name fragments
	ArgParts  [][]string // per-tool argument fragments
}

func (toolCallDeltasInput) Generate(rng *rand.Rand, size int) reflect.Value {
	numTools := rng.Intn(3) + 1
	toolIDs := make([]string, numTools)
	nameParts := make([][]string, numTools)
	argParts := make([][]string, numTools)

	for i := 0; i < numTools; i++ {
		toolIDs[i] = "call_" + randPropAlpha(rng, 8)

		// 1-3 name fragments
		numNameParts := rng.Intn(3) + 1
		nameParts[i] = make([]string, numNameParts)
		for j := range nameParts[i] {
			nameParts[i][j] = randPropAlpha(rng, 5)
		}

		// 1-4 argument fragments
		numArgParts := rng.Intn(4) + 1
		argParts[i] = make([]string, numArgParts)
		for j := range argParts[i] {
			argParts[i][j] = randPropAlpha(rng, 8)
		}
	}

	return reflect.ValueOf(toolCallDeltasInput{
		NumTools:  numTools,
		ToolIDs:   toolIDs,
		NameParts: nameParts,
		ArgParts:  argParts,
	})
}

// --- Helpers ---

// buildPropSSEResponse constructs a synthetic SSE body from ChatCompletionChunk values.
func buildPropSSEResponse(chunks ...ChatCompletionChunk) io.ReadCloser {
	var sb strings.Builder
	for _, chunk := range chunks {
		data, _ := json.Marshal(chunk)
		sb.WriteString("data: ")
		sb.Write(data)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return io.NopCloser(strings.NewReader(sb.String()))
}

// collectPropEvents runs StreamReader and collects all events.
func collectPropEvents(body io.ReadCloser) ([]bond.StreamEvent, []error) {
	var events []bond.StreamEvent
	var errs []error
	StreamReader(context.Background(), body, "test:", func(ev bond.StreamEvent, err error) bool {
		if err != nil {
			errs = append(errs, err)
			return false
		}
		events = append(events, ev)
		return true
	})
	return events, errs
}

// --- Property Tests ---

// Feature: ollama-provider, Property 1: Message mapping preserves content
// Validates: 2.1, 2.2, 2.3, 2.4, 2.5
func TestProperty_MessageMappingPreservesContent(t *testing.T) {
	f := func(input messageInput) bool {
		mapped := MapMessages(input.Messages, "")

		for _, msg := range input.Messages {
			role := "user"
			if msg.Role == bond.RoleAssistant {
				role = "assistant"
			}

			// Collect expected text, tool calls, and tool results from this message
			var expectedText string
			var expectedToolCalls []bond.ToolUseBlock
			var expectedToolResults []bond.ToolResultBlock

			for _, block := range msg.Content {
				switch b := block.(type) {
				case *bond.TextBlock:
					expectedText += b.Text
				case *bond.ToolUseBlock:
					expectedToolCalls = append(expectedToolCalls, *b)
				case *bond.ToolResultBlock:
					expectedToolResults = append(expectedToolResults, *b)
				}
			}

			// Check role preservation for main message (text/tool_calls)
			if expectedText != "" || len(expectedToolCalls) > 0 {
				found := false
				for _, m := range mapped {
					if m.Role == role && m.Content == expectedText && len(m.ToolCalls) == len(expectedToolCalls) {
						allMatch := true
						for i, tc := range expectedToolCalls {
							if m.ToolCalls[i].ID != tc.ID ||
								m.ToolCalls[i].Function.Name != tc.Name ||
								m.ToolCalls[i].Function.Arguments != string(tc.Input) {
								allMatch = false
								break
							}
						}
						if allMatch {
							found = true
							break
						}
					}
				}
				if !found {
					return false
				}
			}

			// Check tool result messages
			for _, tr := range expectedToolResults {
				expectedContent := ""
				for _, b := range tr.Content {
					if tb, ok := b.(*bond.TextBlock); ok {
						expectedContent += tb.Text
					}
				}
				found := false
				for _, m := range mapped {
					if m.Role == "tool" && m.ToolCallID == tr.ToolUseID && m.Content == expectedContent {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("message mapping content preservation property failed: %v", err)
	}
}

// Feature: ollama-provider, Property 2: System prompt prepending
// Validates: 2.6
func TestProperty_SystemPromptPrepended(t *testing.T) {
	f := func(input systemPromptInput) bool {
		mapped := MapMessages(input.Messages, input.SystemPrompt)

		// First message must be the system prompt
		if len(mapped) == 0 {
			return false
		}

		if mapped[0].Role != "system" {
			return false
		}

		if mapped[0].Content != input.SystemPrompt {
			return false
		}

		// Remaining messages must preserve conversation order
		conversationMapped := mapped[1:]

		// No additional system messages should appear
		for _, m := range conversationMapped {
			if m.Role == "system" {
				return false
			}
		}

		// Verify conversation messages maintain input order:
		// Walk through input messages and ensure their content appears in order
		convIdx := 0
		for _, msg := range input.Messages {
			expectedRole := "user"
			if msg.Role == bond.RoleAssistant {
				expectedRole = "assistant"
			}

			// Find the next mapped message matching this role and content
			var expectedText string
			for _, block := range msg.Content {
				if tb, ok := block.(*bond.TextBlock); ok {
					expectedText += tb.Text
				}
			}

			if expectedText == "" {
				continue // messages with only tool results produce "tool" role messages
			}

			found := false
			for convIdx < len(conversationMapped) {
				if conversationMapped[convIdx].Role == expectedRole &&
					conversationMapped[convIdx].Content == expectedText {
					found = true
					convIdx++
					break
				}
				convIdx++
			}
			if !found {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("system prompt prepending property failed: %v", err)
	}
}

// Feature: ollama-provider, Property 3: Tool definition mapping preserves fields
// Validates: 3.2
func TestProperty_ToolDefinitionMapping(t *testing.T) {
	f := func(input toolInput) bool {
		tool := &propMockTool{
			name:   input.ToolName,
			desc:   input.ToolDesc,
			schema: json.RawMessage(input.SchemaJSON),
		}

		mapped, err := MapTools([]bond.Tool{tool})
		if err != nil {
			return false
		}

		if len(mapped) != 1 {
			return false
		}

		mt := mapped[0]

		if mt.Type != "function" {
			return false
		}

		if mt.Function.Name != input.ToolName {
			return false
		}

		if mt.Function.Description != input.ToolDesc {
			return false
		}

		// Parameters should match the schema JSON
		var expectedParams, actualParams any
		if err := json.Unmarshal([]byte(input.SchemaJSON), &expectedParams); err != nil {
			return true // skip invalid schemas
		}
		if err := json.Unmarshal(mt.Function.Parameters, &actualParams); err != nil {
			return false
		}

		expectedBytes, _ := json.Marshal(expectedParams)
		actualBytes, _ := json.Marshal(actualParams)
		return string(expectedBytes) == string(actualBytes)
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("tool definition mapping property failed: %v", err)
	}
}

// Feature: ollama-provider, Property 4: Stream always begins with Start event
// Validates: 5.1
func TestProperty_StreamStartsWithStartEvent(t *testing.T) {
	f := func(input sseChunksInput) bool {
		// Build SSE chunks with content
		var chunks []ChatCompletionChunk
		for _, content := range input.Contents {
			chunks = append(chunks, ChatCompletionChunk{
				ID: "chatcmpl-test",
				Choices: []ChunkChoice{
					{Index: 0, Delta: ChunkDelta{Content: content}},
				},
			})
		}

		body := buildPropSSEResponse(chunks...)
		events, errs := collectPropEvents(body)

		if len(errs) > 0 {
			return false
		}

		if len(events) == 0 {
			return false
		}

		return events[0].Type == bond.StreamEventStart
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("stream starts with start event property failed: %v", err)
	}

	// Also test immediate [DONE] (empty chunk list)
	body := buildPropSSEResponse() // only "data: [DONE]\n\n"
	events, errs := collectPropEvents(body)
	if len(errs) > 0 {
		t.Fatalf("unexpected error for empty stream: %v", errs[0])
	}
	if len(events) == 0 || events[0].Type != bond.StreamEventStart {
		t.Error("empty stream must still start with StreamEventStart")
	}
}

// Feature: ollama-provider, Property 5: Content delta text preservation
// Validates: 5.2
func TestProperty_TextDeltaPreservation(t *testing.T) {
	f := func(input sseChunksInput) bool {
		var chunks []ChatCompletionChunk
		for _, content := range input.Contents {
			chunks = append(chunks, ChatCompletionChunk{
				ID: "chatcmpl-test",
				Choices: []ChunkChoice{
					{Index: 0, Delta: ChunkDelta{Content: content}},
				},
			})
		}

		body := buildPropSSEResponse(chunks...)
		events, errs := collectPropEvents(body)

		if len(errs) > 0 {
			return false
		}

		// Filter to text delta events only
		var textDeltas []bond.StreamEvent
		for _, ev := range events {
			if ev.Type == bond.StreamEventTextDelta {
				textDeltas = append(textDeltas, ev)
			}
		}

		// Must have one TextDelta per content chunk
		if len(textDeltas) != len(input.Contents) {
			return false
		}

		// Each text delta must have the exact content
		for i, td := range textDeltas {
			if td.TextDelta != input.Contents[i] {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("text delta preservation property failed: %v", err)
	}
}

// Feature: ollama-provider, Property 6: Tool call accumulation correctness
// Validates: 5.3, 5.4
func TestProperty_ToolCallAccumulation(t *testing.T) {
	f := func(input toolCallDeltasInput) bool {
		// Build SSE chunks that deliver tool call deltas in fragments.
		var chunks []ChatCompletionChunk

		// Chunk 1: deliver all IDs and first name/arg fragment
		var firstDeltas []ChunkToolCallDelta
		for i := 0; i < input.NumTools; i++ {
			firstDeltas = append(firstDeltas, ChunkToolCallDelta{
				Index: i,
				ID:    input.ToolIDs[i],
				Type:  "function",
				Function: ChunkFunctionDelta{
					Name:      input.NameParts[i][0],
					Arguments: input.ArgParts[i][0],
				},
			})
		}
		chunks = append(chunks, ChatCompletionChunk{
			ID: "chatcmpl-test",
			Choices: []ChunkChoice{
				{Index: 0, Delta: ChunkDelta{ToolCalls: firstDeltas}},
			},
		})

		// Subsequent chunks: deliver remaining name and argument fragments
		maxParts := 0
		for i := 0; i < input.NumTools; i++ {
			if len(input.NameParts[i]) > maxParts {
				maxParts = len(input.NameParts[i])
			}
			if len(input.ArgParts[i]) > maxParts {
				maxParts = len(input.ArgParts[i])
			}
		}

		for partIdx := 1; partIdx < maxParts; partIdx++ {
			var deltas []ChunkToolCallDelta
			for i := 0; i < input.NumTools; i++ {
				var namePart, argPart string
				if partIdx < len(input.NameParts[i]) {
					namePart = input.NameParts[i][partIdx]
				}
				if partIdx < len(input.ArgParts[i]) {
					argPart = input.ArgParts[i][partIdx]
				}
				if namePart != "" || argPart != "" {
					deltas = append(deltas, ChunkToolCallDelta{
						Index: i,
						Function: ChunkFunctionDelta{
							Name:      namePart,
							Arguments: argPart,
						},
					})
				}
			}
			if len(deltas) > 0 {
				chunks = append(chunks, ChatCompletionChunk{
					ID: "chatcmpl-test",
					Choices: []ChunkChoice{
						{Index: 0, Delta: ChunkDelta{ToolCalls: deltas}},
					},
				})
			}
		}

		// Final chunk: finish_reason=tool_calls
		finishReason := "tool_calls"
		chunks = append(chunks, ChatCompletionChunk{
			ID: "chatcmpl-test",
			Choices: []ChunkChoice{
				{Index: 0, FinishReason: &finishReason},
			},
		})

		body := buildPropSSEResponse(chunks...)
		events, errs := collectPropEvents(body)

		if len(errs) > 0 {
			return false
		}

		// Collect ToolUse events
		var toolUseEvents []bond.StreamEvent
		for _, ev := range events {
			if ev.Type == bond.StreamEventToolUse {
				toolUseEvents = append(toolUseEvents, ev)
			}
		}

		if len(toolUseEvents) != input.NumTools {
			return false
		}

		// Verify each tool use event has correct accumulated data
		for i, ev := range toolUseEvents {
			if ev.ToolUse == nil {
				return false
			}

			expectedID := input.ToolIDs[i]
			expectedName := strings.Join(input.NameParts[i], "")
			expectedArgs := strings.Join(input.ArgParts[i], "")

			if ev.ToolUse.ID != expectedID {
				return false
			}

			if ev.ToolUse.Name != expectedName {
				return false
			}

			if string(ev.ToolUse.Input) != expectedArgs {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("tool call accumulation property failed: %v", err)
	}
}

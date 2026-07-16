package dynamostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/nisimpson/bond"
)

// --- Generators ---

// randomASCII generates a random ASCII string of length 1..maxLen.
func randomASCII(r *rand.Rand, maxLen int) string {
	n := r.Intn(maxLen) + 1
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(32 + r.Intn(95)) // printable ASCII
	}
	return string(b)
}

// randomRole returns a random message role.
func randomRole(r *rand.Rand) bond.Role {
	if r.Intn(2) == 0 {
		return bond.RoleUser
	}
	return bond.RoleAssistant
}

// randomBlock generates a random block (TextBlock, ToolUseBlock, or ToolResultBlock).
func randomBlock(r *rand.Rand) bond.Block {
	switch r.Intn(3) {
	case 0:
		return &bond.TextBlock{Text: randomASCII(r, 50)}
	case 1:
		input, _ := json.Marshal(map[string]string{"key": randomASCII(r, 10)})
		return &bond.ToolUseBlock{
			ID:    randomASCII(r, 10),
			Name:  randomASCII(r, 10),
			Input: json.RawMessage(input),
		}
	default:
		return &bond.ToolResultBlock{
			ToolUseID: randomASCII(r, 10),
			IsError:   r.Intn(2) == 0,
			Content:   []bond.Block{&bond.TextBlock{Text: randomASCII(r, 30)}},
		}
	}
}

// randomMessages generates a slice of random messages with TextBlock, ToolUseBlock,
// and ToolResultBlock content suitable for DynamoDB serialization round-trip testing.
func randomMessages(r *rand.Rand, maxCount int) []bond.Message {
	n := r.Intn(maxCount) + 1
	msgs := make([]bond.Message, n)
	for i := range msgs {
		blockCount := r.Intn(3) + 1
		blocks := make([]bond.Block, blockCount)
		for j := range blocks {
			blocks[j] = randomBlock(r)
		}
		msgs[i] = bond.Message{
			Role:    randomRole(r),
			Content: blocks,
		}
	}
	return msgs
}

// randomSessionID generates a non-empty session ID string.
func randomSessionID(r *rand.Rand) string {
	return randomASCII(r, 20)
}

// messagesEqual compares two message slices for deep equality.
func messagesEqual(a, b []bond.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role {
			return false
		}
		if len(a[i].Content) != len(b[i].Content) {
			return false
		}
		for j := range a[i].Content {
			if !reflect.DeepEqual(a[i].Content[j], b[i].Content[j]) {
				return false
			}
		}
	}
	return true
}

// --- Mock DynamoDB Client ---

// errClient is a mock DynamoDBClient that returns a configured error on all operations.
type errClient struct {
	err error
}

func (c *errClient) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return nil, c.err
}

func (c *errClient) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return nil, c.err
}

func (c *errClient) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return nil, c.err
}

// --- Property Tests ---

// Feature: conversation-session-management, Property 13: DynamoDB serialization round-trip
// **Validates: Requirements CONV-8.5**
func TestProperty_DynamoDBSerializationRoundTrip(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		messages := randomMessages(r, 10)

		// Serialize
		data, err := serializeMessages(messages)
		if err != nil {
			t.Logf("serializeMessages failed: %v", err)
			return false
		}

		// Deserialize
		restored, err := deserializeMessages(data)
		if err != nil {
			t.Logf("deserializeMessages failed: %v", err)
			return false
		}

		// Verify round-trip equivalence
		if !messagesEqual(messages, restored) {
			t.Logf("Round-trip mismatch for seed %d", seed)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 13: DynamoDB serialization round-trip — failed: %v", err)
	}
}

// TestProperty_DynamoDBSerializationRoundTripWithMediaURI verifies that MediaBlocks
// with SourceURI survive the round-trip, while reader-based MediaBlocks are omitted.
func TestProperty_DynamoDBSerializationRoundTripWithMediaURI(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))

		// Build messages with a mix of regular blocks and SourceURI-based MediaBlocks
		n := r.Intn(5) + 1
		msgs := make([]bond.Message, n)
		expected := make([]bond.Message, n)

		for i := range msgs {
			blockCount := r.Intn(3) + 1
			blocks := make([]bond.Block, 0, blockCount+1)
			expectedBlocks := make([]bond.Block, 0, blockCount+1)

			for range blockCount {
				b := randomBlock(r)
				blocks = append(blocks, b)
				expectedBlocks = append(expectedBlocks, b)
			}

			// Add a SourceURI-based MediaBlock (should survive round-trip)
			mediaBlock := &bond.MediaBlock{
				Type:      bond.MediaTypeImage,
				MIMEType:  "image/png",
				SourceURI: "s3://bucket/" + randomASCII(r, 20),
			}
			blocks = append(blocks, mediaBlock)
			expectedBlocks = append(expectedBlocks, mediaBlock)

			// Add a reader-based MediaBlock (should be omitted)
			readerBlock := &bond.MediaBlock{
				Type:     bond.MediaTypeImage,
				MIMEType: "image/jpeg",
				Source:   strings.NewReader("fake image data"),
			}
			blocks = append(blocks, readerBlock)
			// readerBlock is NOT added to expectedBlocks — it should be omitted

			msgs[i] = bond.Message{Role: randomRole(r), Content: blocks}
			expected[i] = bond.Message{Role: randomRole(r), Content: expectedBlocks}
			expected[i].Role = msgs[i].Role
		}

		// Serialize
		data, err := serializeMessages(msgs)
		if err != nil {
			t.Logf("serializeMessages failed: %v", err)
			return false
		}

		// Deserialize
		restored, err := deserializeMessages(data)
		if err != nil {
			t.Logf("deserializeMessages failed: %v", err)
			return false
		}

		// Verify that restored matches expected (without reader-based media)
		if !messagesEqual(expected, restored) {
			t.Logf("Round-trip with media mismatch for seed %d", seed)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 13: DynamoDB serialization round-trip with MediaURI — failed: %v", err)
	}
}

// Feature: conversation-session-management, Property 14: DynamoDB error wrapping
// **Validates: Requirements CONV-8.6**
func TestProperty_DynamoDBErrorWrapping(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))

		sessionID := randomSessionID(r)
		originalErr := fmt.Errorf("simulated DynamoDB error: %s", randomASCII(r, 20))

		client := &errClient{err: originalErr}
		store := NewSessionStore(Options{
			Client:    client,
			TableName: "test-table",
		})

		ctx := context.Background()

		// Test Load error wrapping
		_, loadErr := store.Load(ctx, sessionID)
		if loadErr == nil {
			t.Logf("Load should have returned an error for session %q", sessionID)
			return false
		}

		// Verify the wrapped error contains the operation name
		if !strings.Contains(loadErr.Error(), "load") {
			t.Logf("Load error should contain operation name 'load': %v", loadErr)
			return false
		}

		// Verify the wrapped error contains the session ID (quoted with %q in the format string)
		quotedID := fmt.Sprintf("%q", sessionID)
		if !strings.Contains(loadErr.Error(), quotedID) {
			t.Logf("Load error should contain session ID %s: %v", quotedID, loadErr)
			return false
		}

		// Verify errors.Is works to unwrap to the original error
		if !errors.Is(loadErr, originalErr) {
			t.Logf("errors.Is(loadErr, originalErr) should be true for session %q", sessionID)
			return false
		}

		// Test Save error wrapping
		msgs := randomMessages(r, 3)
		saveErr := store.Save(ctx, sessionID, msgs)
		if saveErr == nil {
			t.Logf("Save should have returned an error for session %q", sessionID)
			return false
		}

		// Verify the wrapped error contains the operation name
		if !strings.Contains(saveErr.Error(), "save") {
			t.Logf("Save error should contain operation name 'save': %v", saveErr)
			return false
		}

		// Verify the wrapped error contains the session ID (quoted with %q in the format string)
		if !strings.Contains(saveErr.Error(), quotedID) {
			t.Logf("Save error should contain session ID %s: %v", quotedID, saveErr)
			return false
		}

		// Verify errors.Is works to unwrap to the original error
		if !errors.Is(saveErr, originalErr) {
			t.Logf("errors.Is(saveErr, originalErr) should be true for session %q", sessionID)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 14: DynamoDB error wrapping — failed: %v", err)
	}
}

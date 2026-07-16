package dynamostore

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nisimpson/bond"
	"github.com/nisimpson/bond/extra/session"
)

// maxItemSize is the DynamoDB item size limit (400KB).
const maxItemSize = 400 * 1024

// SessionStore implements [session.Store] backed by DynamoDB.
type SessionStore struct {
	client    DynamoDBClient
	tableName string
	ttl       time.Duration
	keyPrefix string
}

// compile-time interface check
var _ session.Store = (*SessionStore)(nil)

// NewSessionStore creates a DynamoDB-backed session store.
func NewSessionStore(opts Options) *SessionStore {
	return &SessionStore{
		client:    opts.Client,
		tableName: opts.TableName,
		ttl:       opts.TTL,
		keyPrefix: opts.KeyPrefix,
	}
}

// Load retrieves the stored messages for the given session.
// Returns an empty slice (not nil) and nil error if no data exists for the session ID.
func (s *SessionStore) Load(ctx context.Context, sessionID string) ([]bond.Message, error) {
	pk := prefixedKey(s.keyPrefix, sessionID)
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamostore: load session %q: %w", sessionID, err)
	}

	if out.Item == nil {
		return []bond.Message{}, nil
	}

	msgAttr, ok := out.Item["messages"]
	if !ok {
		return []bond.Message{}, nil
	}

	bAttr, ok := msgAttr.(*types.AttributeValueMemberB)
	if !ok {
		return []bond.Message{}, nil
	}

	messages, err := deserializeMessages(bAttr.Value)
	if err != nil {
		return nil, fmt.Errorf("dynamostore: load session %q: %w", sessionID, err)
	}

	return messages, nil
}

// Save persists the message slice for the given session, overwriting any previous data.
func (s *SessionStore) Save(ctx context.Context, sessionID string, messages []bond.Message) error {
	data, err := serializeMessages(messages)
	if err != nil {
		return fmt.Errorf("dynamostore: save session %q: %w", sessionID, err)
	}

	if len(data) > maxItemSize {
		return fmt.Errorf("dynamostore: save session %q: serialized payload exceeds 400KB limit (%d bytes)", sessionID, len(data))
	}

	pk := prefixedKey(s.keyPrefix, sessionID)
	item := map[string]types.AttributeValue{
		"pk":       &types.AttributeValueMemberS{Value: pk},
		"messages": &types.AttributeValueMemberB{Value: data},
	}

	if s.ttl > 0 {
		expiry := time.Now().Add(s.ttl).Unix()
		item["ttl"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiry)}
	}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("dynamostore: save session %q: %w", sessionID, err)
	}

	return nil
}

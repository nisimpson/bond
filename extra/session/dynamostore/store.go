// Package dynamostore provides a DynamoDB-backed implementation of [session.SessionStore].
// It isolates the AWS SDK dependency from the session core package.
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

// Requirement: CONV-8.1, CONV-8.2, CONV-8.3, CONV-8.4, CONV-8.5, CONV-8.6, CONV-8.7 — DynamoDB session store

// maxItemSize is the DynamoDB item size limit (400KB).
const maxItemSize = 400 * 1024

// DynamoDBClient is the subset of the DynamoDB API used by the store.
type DynamoDBClient interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// Options configures the DynamoDB session store.
type Options struct {
	// Client is the DynamoDB API client.
	Client DynamoDBClient
	// TableName is the DynamoDB table to use.
	TableName string
	// TTL is the optional duration after which sessions expire.
	// Zero means no expiration.
	TTL time.Duration
}

// Store implements session.SessionStore backed by DynamoDB.
type Store struct {
	client    DynamoDBClient
	tableName string
	ttl       time.Duration
}

// compile-time interface check
var _ session.SessionStore = (*Store)(nil)

// New creates a DynamoDB-backed session store.
func New(opts Options) *Store {
	return &Store{
		client:    opts.Client,
		tableName: opts.TableName,
		ttl:       opts.TTL,
	}
}

// Load retrieves the stored messages for the given session.
// Returns an empty slice (not nil) and nil error if no data exists for the session ID.
func (s *Store) Load(ctx context.Context, sessionID string) ([]bond.Message, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: sessionID},
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
func (s *Store) Save(ctx context.Context, sessionID string, messages []bond.Message) error {
	data, err := serializeMessages(messages)
	if err != nil {
		return fmt.Errorf("dynamostore: save session %q: %w", sessionID, err)
	}

	if len(data) > maxItemSize {
		return fmt.Errorf("dynamostore: save session %q: serialized payload exceeds 400KB limit (%d bytes)", sessionID, len(data))
	}

	item := map[string]types.AttributeValue{
		"pk":       &types.AttributeValueMemberS{Value: sessionID},
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

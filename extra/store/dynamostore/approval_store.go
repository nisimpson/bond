package dynamostore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nisimpson/bond/extra/approval"
)

// ApprovalStore implements [approval.Store] backed by DynamoDB.
type ApprovalStore struct {
	client    DynamoDBClient
	tableName string
	ttl       time.Duration
	keyPrefix string
}

// compile-time interface check
var _ approval.Store = (*ApprovalStore)(nil)

// NewApprovalStore creates a DynamoDB-backed approval record store.
func NewApprovalStore(opts Options) *ApprovalStore {
	return &ApprovalStore{
		client:    opts.Client,
		tableName: opts.TableName,
		ttl:       opts.TTL,
		keyPrefix: opts.KeyPrefix,
	}
}

// Load retrieves an approval record by ID. Returns (nil, nil) if not found.
func (s *ApprovalStore) Load(ctx context.Context, id string) (*approval.Record, error) {
	pk := prefixedKey(s.keyPrefix, id)
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamostore: load approval %q: %w", id, err)
	}

	if out.Item == nil {
		return nil, nil
	}

	dataAttr, ok := out.Item["data"]
	if !ok {
		return nil, nil
	}

	bAttr, ok := dataAttr.(*types.AttributeValueMemberB)
	if !ok {
		return nil, nil
	}

	var record approval.Record
	if err := json.Unmarshal(bAttr.Value, &record); err != nil {
		return nil, fmt.Errorf("dynamostore: load approval %q: %w", id, err)
	}

	return &record, nil
}

// Save persists an approval record, overwriting any existing record with the same ID.
func (s *ApprovalStore) Save(ctx context.Context, record *approval.Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("dynamostore: save approval %q: %w", record.ID, err)
	}

	pk := prefixedKey(s.keyPrefix, record.ID)
	item := map[string]types.AttributeValue{
		"pk":   &types.AttributeValueMemberS{Value: pk},
		"data": &types.AttributeValueMemberB{Value: data},
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
		return fmt.Errorf("dynamostore: save approval %q: %w", record.ID, err)
	}

	return nil
}

// Delete removes an approval record by ID. No-op if not found.
func (s *ApprovalStore) Delete(ctx context.Context, id string) error {
	pk := prefixedKey(s.keyPrefix, id)
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return fmt.Errorf("dynamostore: delete approval %q: %w", id, err)
	}

	return nil
}

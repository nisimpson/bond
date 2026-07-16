package dynamostore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nisimpson/bond/agent"
)

// CheckpointStore implements [agent.CheckpointStore] backed by DynamoDB.
type CheckpointStore struct {
	client    DynamoDBClient
	tableName string
	ttl       time.Duration
	keyPrefix string
}

// compile-time interface check
var _ agent.CheckpointStore = (*CheckpointStore)(nil)

// NewCheckpointStore creates a DynamoDB-backed checkpoint store.
func NewCheckpointStore(opts Options) *CheckpointStore {
	return &CheckpointStore{
		client:    opts.Client,
		tableName: opts.TableName,
		ttl:       opts.TTL,
		keyPrefix: opts.KeyPrefix,
	}
}

// Save persists a snapshot, overwriting any existing snapshot with the same ID.
func (s *CheckpointStore) Save(ctx context.Context, id string, snapshot *agent.Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("dynamostore: save checkpoint %q: %w", id, err)
	}

	pk := prefixedKey(s.keyPrefix, id)
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
		return fmt.Errorf("dynamostore: save checkpoint %q: %w", id, err)
	}

	return nil
}

// Load retrieves a snapshot by ID. Returns [agent.ErrSnapshotNotFound]
// if the snapshot does not exist.
func (s *CheckpointStore) Load(ctx context.Context, id string) (*agent.Snapshot, error) {
	pk := prefixedKey(s.keyPrefix, id)
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamostore: load checkpoint %q: %w", id, err)
	}

	if out.Item == nil {
		return nil, agent.ErrSnapshotNotFound
	}

	dataAttr, ok := out.Item["data"]
	if !ok {
		return nil, agent.ErrSnapshotNotFound
	}

	bAttr, ok := dataAttr.(*types.AttributeValueMemberB)
	if !ok {
		return nil, agent.ErrSnapshotNotFound
	}

	var snapshot agent.Snapshot
	if err := json.Unmarshal(bAttr.Value, &snapshot); err != nil {
		return nil, fmt.Errorf("dynamostore: load checkpoint %q: %w", id, err)
	}

	return &snapshot, nil
}

// Delete removes a snapshot by ID. No-op if not found.
func (s *CheckpointStore) Delete(ctx context.Context, id string) error {
	pk := prefixedKey(s.keyPrefix, id)
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return fmt.Errorf("dynamostore: delete checkpoint %q: %w", id, err)
	}

	return nil
}

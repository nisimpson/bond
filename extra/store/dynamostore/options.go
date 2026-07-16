// Package dynamostore provides DynamoDB-backed implementations of bond storage
// interfaces: [session.Store], [approval.Store], and [agent.CheckpointStore].
// All implementations use a single DynamoDB table with partition key prefixes
// to isolate different storage concerns.
package dynamostore

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// DynamoDBClient is the subset of the DynamoDB API used by the store implementations.
type DynamoDBClient interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// Options configures the DynamoDB store implementations.
type Options struct {
	// Client is the DynamoDB API client.
	Client DynamoDBClient
	// TableName is the DynamoDB table to use.
	TableName string
	// TTL is the optional duration after which items expire.
	// Zero means no expiration.
	TTL time.Duration
	// KeyPrefix is prepended to all partition keys to namespace items.
	// Useful when multiple store types share one table.
	// If empty, no prefix is added.
	KeyPrefix string
}

// prefixedKey returns the full partition key with optional prefix.
func prefixedKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "#" + key
}

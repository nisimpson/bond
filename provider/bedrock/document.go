package bedrock

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
)

// emptyObjectDoc is a pre-built document representing `{}`.
// Bedrock requires tool input to always be a non-nil object.
var emptyObjectDoc = document.NewLazyDocument(map[string]any{})

// toDocument converts json.RawMessage to a Bedrock document.Interface.
// Returns an empty object document if raw is nil or empty, since Bedrock
// rejects tool use blocks with nil input.
func toDocument(raw json.RawMessage) document.Interface {
	if len(raw) == 0 {
		return emptyObjectDoc
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return emptyObjectDoc
	}
	return document.NewLazyDocument(v)
}

// toDocumentFromBytes converts raw JSON bytes to a Bedrock document.Interface.
func toDocumentFromBytes(data []byte) document.Interface {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	return document.NewLazyDocument(v)
}

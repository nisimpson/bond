// Package schema provides JSON Schema generation and validation helpers
// for helix tools. It wraps github.com/google/jsonschema-go/jsonschema to
// derive schemas from Go types and validate data against them.
package schema

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// Schema wraps a generated JSON Schema and implements json.Marshaler.
type Schema struct {
	schema *jsonschema.Schema
}

// MarshalJSON implements json.Marshaler.
func (s Schema) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.schema)
}

// Validate checks whether data conforms to the schema. Returns an error
// describing all validation failures, or nil if valid.
func (s Schema) Validate(data any) error {
	rs, err := s.schema.Resolve(nil)
	if err != nil {
		return err
	}
	return rs.Validate(data)
}

// For derives a JSON Schema from the Go type T using struct tags
// (json, jsonschema). Returns a Schema suitable for use as
// helix.FuncToolOptions.InputSchema or OutputSchema.
//
// Example:
//
//	type AddInput struct {
//	    A int `json:"a" jsonschema:"first number"`
//	    B int `json:"b" jsonschema:"second number"`
//	}
//
//	schema.For[AddInput]() // returns json.Marshaler with the derived schema
func For[T any]() Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic("schema: failed to infer schema: " + err.Error())
	}
	return Schema{schema: s}
}

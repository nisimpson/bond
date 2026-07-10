package delegation

import (
	"encoding/json"
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// metadataKey is the key used in A2A message metadata to carry delegation skills.
const metadataKey = "delegation:skills"

// wireSkill is the JSON-serializable representation of a Skill in message metadata.
type wireSkill struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// attachSkills attaches delegation skills to an A2A message's metadata.
// Call this on the client side before sending the message to the server.
func attachSkills(msg *a2a.Message, skills []Skill) error {
	wire := make([]wireSkill, len(skills))
	for i, s := range skills {
		var schema json.RawMessage
		if s.InputSchema != nil {
			data, err := s.InputSchema.MarshalJSON()
			if err != nil {
				return fmt.Errorf("delegation: marshal schema for %q: %w", s.Name, err)
			}
			schema = data
		}
		wire[i] = wireSkill{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: schema,
		}
	}

	data, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("delegation: marshal skills: %w", err)
	}

	if msg.Metadata == nil {
		msg.Metadata = make(map[string]any)
	}
	msg.Metadata[metadataKey] = json.RawMessage(data)
	return nil
}

// extractSkills extracts delegation skills from an A2A message's metadata.
// Call this on the server side when receiving a message from the client.
// Returns nil if no skills are present.
func extractSkills(msg *a2a.Message) ([]Skill, error) {
	if msg == nil || msg.Metadata == nil {
		return nil, nil
	}

	raw, exists := msg.Metadata[metadataKey]
	if !exists {
		return nil, nil
	}

	// Handle both json.RawMessage and pre-decoded types.
	var data []byte
	switch v := raw.(type) {
	case json.RawMessage:
		data = v
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		// May have been decoded as any; re-marshal it.
		var err error
		data, err = json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("delegation: extract skills: %w", err)
		}
	}

	var wire []wireSkill
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("delegation: unmarshal skills: %w", err)
	}

	skills := make([]Skill, len(wire))
	for i, w := range wire {
		skills[i] = Skill{
			Name:        w.Name,
			Description: w.Description,
			InputSchema: rawSchema(w.InputSchema),
		}
	}
	return skills, nil
}

// rawSchema wraps json.RawMessage as a json.Marshaler.
type rawSchema json.RawMessage

func (r rawSchema) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte(`{"type":"object"}`), nil
	}
	return json.RawMessage(r), nil
}

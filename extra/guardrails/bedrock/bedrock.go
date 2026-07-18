// Package bedrock provides a [guardrails.ContentFilter] backed by the
// Amazon Bedrock ApplyGuardrail API. It evaluates content against a
// managed guardrail configuration and maps the response to guardrails
// filter actions.
package bedrock

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/nisimpson/bond/extra/guardrails"
)

// Client defines the subset of the Bedrock Runtime client used for
// guardrail evaluation. This allows mocking for tests.
type Client interface {
	ApplyGuardrail(ctx context.Context, params *bedrockruntime.ApplyGuardrailInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ApplyGuardrailOutput, error)
}

// Options configures the Bedrock guardrail filter.
type Options struct {
	// Client is the Bedrock Runtime client.
	Client Client
	// GuardrailID is the identifier of the guardrail to apply.
	GuardrailID string
	// Version is the guardrail version. Defaults to "DRAFT".
	Version string
}

// Filter implements [guardrails.ContentFilter] using the Bedrock
// ApplyGuardrail API.
type Filter struct {
	client      Client
	guardrailID string
	version     string
}

// NewFilter creates a Bedrock guardrail filter with the given options.
func NewFilter(opts Options) *Filter {
	version := opts.Version
	if version == "" {
		version = "DRAFT"
	}
	return &Filter{
		client:      opts.Client,
		guardrailID: opts.GuardrailID,
		version:     version,
	}
}

// Name implements [guardrails.ContentFilter].
func (f *Filter) Name() string { return "bedrock_guardrail" }

// Filter implements [guardrails.ContentFilter]. It calls the
// ApplyGuardrail API and maps the response to a [guardrails.FilterResult].
func (f *Filter) Filter(ctx context.Context, content string, direction guardrails.Direction) guardrails.FilterResult {
	source := mapSource(direction)

	input := &bedrockruntime.ApplyGuardrailInput{
		GuardrailIdentifier: aws.String(f.guardrailID),
		GuardrailVersion:    aws.String(f.version),
		Source:              source,
		Content: []types.GuardrailContentBlock{
			&types.GuardrailContentBlockMemberText{
				Value: types.GuardrailTextBlock{
					Text:       aws.String(content),
					Qualifiers: []types.GuardrailContentQualifier{},
				},
			},
		},
	}

	output, err := f.client.ApplyGuardrail(ctx, input)
	if err != nil {
		// On API error, default to warn (fail-open) and let local filters
		// handle it. The caller can add an OnWarn callback to surface this.
		return guardrails.FilterResult{
			Action: guardrails.ActionWarn,
			Reason: "bedrock guardrail API error: " + err.Error(),
		}
	}

	switch output.Action {
	case types.GuardrailActionGuardrailIntervened:
		// Check if any output contains anonymized/redacted text.
		redacted := extractOutputText(output.Outputs)
		if redacted != "" && redacted != content {
			return guardrails.FilterResult{
				Action:   guardrails.ActionRedact,
				Reason:   extractAssessmentReason(output),
				Redacted: redacted,
			}
		}
		return guardrails.FilterResult{
			Action: guardrails.ActionBlock,
			Reason: extractAssessmentReason(output),
		}
	default:
		return guardrails.FilterResult{Action: guardrails.ActionAllow}
	}
}

// mapSource converts a guardrails direction to the Bedrock source type.
func mapSource(dir guardrails.Direction) types.GuardrailContentSource {
	switch dir {
	case guardrails.DirectionOutput:
		return types.GuardrailContentSourceOutput
	default:
		return types.GuardrailContentSourceInput
	}
}

// extractOutputText concatenates text from all guardrail outputs.
func extractOutputText(outputs []types.GuardrailOutputContent) string {
	var sb strings.Builder
	for _, o := range outputs {
		if o.Text != nil {
			sb.WriteString(*o.Text)
		}
	}
	return sb.String()
}

// extractAssessmentReason builds a reason string from the guardrail
// assessments.
func extractAssessmentReason(output *bedrockruntime.ApplyGuardrailOutput) string {
	if len(output.Assessments) == 0 {
		return "guardrail intervened"
	}

	var reasons []string

	for _, assessment := range output.Assessments {
		// Topic policy assessments
		if assessment.TopicPolicy != nil {
			for _, topic := range assessment.TopicPolicy.Topics {
				if topic.Name != nil && topic.Action == types.GuardrailTopicPolicyActionBlocked {
					reasons = append(reasons, "topic: "+*topic.Name)
				}
			}
		}

		// Content policy assessments
		if assessment.ContentPolicy != nil {
			for _, filter := range assessment.ContentPolicy.Filters {
				if filter.Action == types.GuardrailContentPolicyActionBlocked {
					reasons = append(reasons, "content: "+string(filter.Type))
				}
			}
		}

		// Sensitive information policy
		if assessment.SensitiveInformationPolicy != nil {
			for _, pii := range assessment.SensitiveInformationPolicy.PiiEntities {
				if pii.Action == types.GuardrailSensitiveInformationPolicyActionBlocked {
					reasons = append(reasons, "pii: "+string(pii.Type))
				} else if pii.Action == types.GuardrailSensitiveInformationPolicyActionAnonymized {
					reasons = append(reasons, "pii_anonymized: "+string(pii.Type))
				}
			}
			for _, regex := range assessment.SensitiveInformationPolicy.Regexes {
				if regex.Action == types.GuardrailSensitiveInformationPolicyActionBlocked {
					name := "regex"
					if regex.Name != nil {
						name = *regex.Name
					}
					reasons = append(reasons, "regex: "+name)
				}
			}
		}

		// Word policy
		if assessment.WordPolicy != nil {
			for _, word := range assessment.WordPolicy.CustomWords {
				if word.Action == types.GuardrailWordPolicyActionBlocked && word.Match != nil {
					reasons = append(reasons, "word: "+*word.Match)
				}
			}
			for _, word := range assessment.WordPolicy.ManagedWordLists {
				if word.Action == types.GuardrailWordPolicyActionBlocked {
					reasons = append(reasons, "managed_word: "+string(word.Type))
				}
			}
		}
	}

	if len(reasons) == 0 {
		return "guardrail intervened"
	}
	return strings.Join(reasons, "; ")
}

// Verify interface compliance.
var _ guardrails.ContentFilter = (*Filter)(nil)

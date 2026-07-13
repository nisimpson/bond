package guardrails

import (
	"context"
	"regexp"
	"strings"
)

// TopicFilter blocks content that matches any of the configured denied topics.
// Topics are matched as case-insensitive substrings.
type TopicFilter struct {
	// Topics is the deny-list of topics to block.
	Topics []string
}

// NewTopicFilter creates a [TopicFilter] with the given denied topics.
func NewTopicFilter(topics ...string) *TopicFilter {
	lower := make([]string, len(topics))
	for i, t := range topics {
		lower[i] = strings.ToLower(t)
	}
	return &TopicFilter{Topics: lower}
}

// Name implements [ContentFilter].
func (f *TopicFilter) Name() string { return "topic_filter" }

// Filter implements [ContentFilter].
func (f *TopicFilter) Filter(_ context.Context, content string, _ Direction) FilterResult {
	lower := strings.ToLower(content)
	for _, topic := range f.Topics {
		if strings.Contains(lower, topic) {
			return FilterResult{
				Action: ActionBlock,
				Reason: "denied topic: " + topic,
			}
		}
	}
	return FilterResult{Action: ActionAllow}
}

// Pattern defines a regex pattern with a name and the action to take on match.
type Pattern struct {
	// Name identifies this pattern.
	Name string
	// Regex is the compiled pattern to match against content.
	Regex *regexp.Regexp
	// Action is what to do when the pattern matches. Defaults to ActionRedact.
	Action ActionType
	// Replacement is the text to substitute when Action == ActionRedact.
	// Defaults to "[REDACTED]".
	Replacement string
}

// PatternFilter scans content for regex patterns and takes configured actions.
type PatternFilter struct {
	patterns []Pattern
}

// NewPatternFilter creates a [PatternFilter] with the given patterns.
func NewPatternFilter(patterns ...Pattern) *PatternFilter {
	return &PatternFilter{patterns: patterns}
}

// Name implements [ContentFilter].
func (f *PatternFilter) Name() string { return "pattern_filter" }

// Filter implements [ContentFilter].
func (f *PatternFilter) Filter(_ context.Context, content string, _ Direction) FilterResult {
	for _, p := range f.patterns {
		if p.Regex.MatchString(content) {
			action := p.Action
			if action == "" {
				action = ActionRedact
			}

			replacement := p.Replacement
			if replacement == "" {
				replacement = "[REDACTED]"
			}

			redacted := ""
			if action == ActionRedact {
				redacted = p.Regex.ReplaceAllString(content, replacement)
			}

			return FilterResult{
				Action:   action,
				Reason:   "pattern matched: " + p.Name,
				Redacted: redacted,
			}
		}
	}
	return FilterResult{Action: ActionAllow}
}

// CommonPIIPatterns returns a set of patterns for common PII types:
// US Social Security numbers, email addresses, and US phone numbers.
func CommonPIIPatterns() []Pattern {
	return []Pattern{
		{
			Name:        "ssn",
			Regex:       regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			Action:      ActionRedact,
			Replacement: "[SSN REDACTED]",
		},
		{
			Name:        "email",
			Regex:       regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
			Action:      ActionRedact,
			Replacement: "[EMAIL REDACTED]",
		},
		{
			Name:        "phone_us",
			Regex:       regexp.MustCompile(`\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`),
			Action:      ActionRedact,
			Replacement: "[PHONE REDACTED]",
		},
	}
}

// KeywordFilter blocks content containing any of the specified keywords.
// Matching is case-insensitive and uses word boundaries.
type KeywordFilter struct {
	keywords []*regexp.Regexp
	raw      []string
}

// NewKeywordFilter creates a [KeywordFilter] with word-boundary matching.
func NewKeywordFilter(keywords ...string) *KeywordFilter {
	compiled := make([]*regexp.Regexp, 0, len(keywords))
	for _, kw := range keywords {
		pattern := `(?i)\b` + regexp.QuoteMeta(kw) + `\b`
		compiled = append(compiled, regexp.MustCompile(pattern))
	}
	return &KeywordFilter{keywords: compiled, raw: keywords}
}

// Name implements [ContentFilter].
func (f *KeywordFilter) Name() string { return "keyword_filter" }

// Filter implements [ContentFilter].
func (f *KeywordFilter) Filter(_ context.Context, content string, _ Direction) FilterResult {
	for i, re := range f.keywords {
		if re.MatchString(content) {
			return FilterResult{
				Action: ActionBlock,
				Reason: "blocked keyword: " + f.raw[i],
			}
		}
	}
	return FilterResult{Action: ActionAllow}
}

// Verify interface compliance.
var (
	_ ContentFilter = (*TopicFilter)(nil)
	_ ContentFilter = (*PatternFilter)(nil)
	_ ContentFilter = (*KeywordFilter)(nil)
)

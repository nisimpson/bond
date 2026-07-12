package acpproxy

import "time"

// Requirement: 10.1 — constructor accepts Transport and client-specific options
// Requirement: 10.3 — accept working directory option
// Requirement: 10.4 — accept Permission_Tier or custom Permission_Policy
// Requirement: 10.5 — accept system prompt and initial context

// ClientOptions configures the ACP client.
type ClientOptions struct {
	// WorkingDir is the CWD sent to the external agent during session creation.
	WorkingDir string

	// SystemPrompt is sent as the first prompt after session creation.
	// If empty, no system prompt is sent.
	SystemPrompt string

	// InitialContext contains additional messages sent after the system prompt
	// to prime the external agent's conversation context.
	InitialContext []string

	// PermissionTier is the built-in permission level for tool approval.
	// Defaults to TierYOLO (zero value) when unset.
	PermissionTier PermissionTier

	// PermissionPolicy is a custom callback that overrides tier-based logic.
	// If set, PermissionTier is ignored.
	PermissionPolicy PermissionPolicy

	// CancelTimeout is how long to wait for the external agent to acknowledge
	// a cancellation before forcefully terminating the stream.
	// Defaults to 5 seconds if zero.
	CancelTimeout time.Duration
}

// AgentInfo contains identifying information about the external agent,
// reported during protocol initialization.
type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities describes what the external agent supports.
type Capabilities struct {
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
}

// PromptCapabilities describes the external agent's prompt features.
type PromptCapabilities struct {
	TextSupported bool `json:"textSupported"`
}

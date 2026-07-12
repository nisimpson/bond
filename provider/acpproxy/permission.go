package acpproxy

import (
	"context"
	"encoding/json"
	"strings"
)

// Requirement: 5.2 — THE ACP_Client SHALL support three built-in Permission_Tiers:
// Read (approve read-only tool categories), Trust (approve read and write tool categories),
// and YOLO (approve all tools without prompting).

// PermissionTier defines the built-in permission levels.
// TierYOLO is the zero value (default) so that an unset PermissionTier
// field in ClientOptions naturally defaults to approving all tools.
type PermissionTier int

const (
	TierYOLO  PermissionTier = iota // Auto-approve all tools (default)
	TierTrust                       // Auto-approve read + write tools
	TierRead                        // Auto-approve read-only tools
)

// Requirement: 5.1 — WHEN the External_Agent sends a `session/request_permission` request,
// THE ACP_Client SHALL invoke the configured Permission_Policy with the tool name, input
// parameters, and the active Permission_Tier.

// PermissionRequest contains the information about a tool the external agent
// wants to execute.
type PermissionRequest struct {
	ToolName   string
	ToolCallID string
	Input      json.RawMessage
	Tier       PermissionTier
}

// Requirement: 5.3, 5.4 — WHEN the Permission_Policy returns an "approve" decision, THE
// ACP_Client SHALL respond with outcome "selected". WHEN it returns "deny", THE ACP_Client
// SHALL respond with outcome "cancelled".

// PermissionDecision is the result of a permission policy evaluation.
type PermissionDecision int

const (
	Approve PermissionDecision = iota
	Deny
)

// Requirement: 5.6, 5.7 — THE ACP_Client SHALL accept a custom Permission_Policy callback
// that overrides the tier-based logic. THE Permission_Policy SHALL receive a context that
// is cancelled if the prompt turn is cancelled.

// PermissionPolicy is a callback that decides whether to approve or deny
// a tool execution request from the external agent.
type PermissionPolicy func(ctx context.Context, req PermissionRequest) PermissionDecision

// readPatterns are substrings that identify read-only tools.
var readPatterns = []string{
	"read", "list", "get", "search", "find",
	"view", "show", "describe", "cat", "head",
	"tail", "ls", "stat",
}

// writePatterns are substrings that identify write tools.
var writePatterns = []string{
	"write", "create", "update", "delete", "remove",
	"edit", "modify", "put", "post", "set",
	"mkdir", "mv", "cp", "rename",
}

// isReadTool returns true if the lowercased tool name contains any read pattern.
func isReadTool(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range readPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// isWriteTool returns true if the lowercased tool name contains any write pattern.
func isWriteTool(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range writePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// tierPolicy returns a PermissionPolicy that makes decisions based on the given tier.
//
// Requirement: 5.2 — Read tier approves read-only tools, Trust tier approves read and
// write tools, YOLO tier approves all tools.
// Requirement: 5.5 — Default to YOLO tier (approve all) as fallback.
func tierPolicy(tier PermissionTier) PermissionPolicy {
	return func(ctx context.Context, req PermissionRequest) PermissionDecision {
		switch tier {
		case TierYOLO:
			return Approve
		case TierTrust:
			if isReadTool(req.ToolName) || isWriteTool(req.ToolName) {
				return Approve
			}
			return Deny
		case TierRead:
			if isReadTool(req.ToolName) {
				return Approve
			}
			return Deny
		default:
			return Approve // default to YOLO behavior
		}
	}
}

// ReadPolicy returns a PermissionPolicy that only approves read-only tools.
func ReadPolicy() PermissionPolicy {
	return tierPolicy(TierRead)
}

// TrustPolicy returns a PermissionPolicy that approves read and write tools.
func TrustPolicy() PermissionPolicy {
	return tierPolicy(TierTrust)
}

// YOLOPolicy returns a PermissionPolicy that approves all tools unconditionally.
func YOLOPolicy() PermissionPolicy {
	return tierPolicy(TierYOLO)
}

package approval

import "time"

// Status represents the state of an approval request.
type Status string

const (
	// StatusPending indicates the approval request is awaiting a decision.
	StatusPending Status = "pending"
	// StatusApproved indicates the approval request was granted.
	StatusApproved Status = "approved"
	// StatusDenied indicates the approval request was rejected.
	StatusDenied Status = "denied"
)

// Record is a serializable record of an approval request. It captures the
// hook event type, the decision status, and an optional reason for denial.
type Record struct {
	ID        string    `json:"id"`
	EventType string    `json:"event_type"`
	Status    Status    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

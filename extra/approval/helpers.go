package approval

import (
	"context"
	"fmt"
)

// ApprovePending marks a pending record as approved. Returns an error if the
// record is not found.
func ApprovePending(ctx context.Context, store Store, id string) error {
	record, err := store.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("approval: load record %q: %w", id, err)
	}
	if record == nil {
		return fmt.Errorf("approval: record %q not found", id)
	}
	record.Status = StatusApproved
	if err := store.Save(ctx, record); err != nil {
		return fmt.Errorf("approval: save record %q: %w", id, err)
	}
	return nil
}

// DenyPending marks a pending record as denied with a reason. Returns an error
// if the record is not found.
func DenyPending(ctx context.Context, store Store, id string, reason string) error {
	record, err := store.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("approval: load record %q: %w", id, err)
	}
	if record == nil {
		return fmt.Errorf("approval: record %q not found", id)
	}
	record.Status = StatusDenied
	record.Reason = reason
	if err := store.Save(ctx, record); err != nil {
		return fmt.Errorf("approval: save record %q: %w", id, err)
	}
	return nil
}

package domain

import (
	"context"
	"time"
)

// Repository is the inventory's storage.
//
// Diff and CloseWindow take a callback so the decision and the write share
// one transaction: the diff must not be made against an inventory that
// another snapshot changes under it, and the outbox event must land with the
// window it announces or not at all. It is the shape the relay module's
// Heartbeat already uses for the lease.
type Repository interface {
	// Diff loads the household's inventory, hands it to decide, and writes
	// back what comes out, all inside one transaction.
	Diff(ctx context.Context, tenantID string, decide func(Snapshot) (Changes, error)) (Changes, error)
	// DueWindows returns the windows whose 2 minutes are up and that have
	// not been announced yet.
	DueWindows(ctx context.Context, now time.Time, limit int) ([]Window, error)
	// CloseWindow marks a window announced and calls announce in the same
	// transaction, so the event and the closing commit together.
	CloseWindow(ctx context.Context, w Window, now time.Time, announce func(context.Context, Window) error) error
	// Inventory lists a household's devices and addresses.
	Inventory(ctx context.Context, tenantID string) ([]Device, []MACRecord, error)
	// Window returns one window of a household, closed or not. The alerts
	// module reads it to resolve "1" back to the device it numbered.
	Window(ctx context.Context, tenantID, windowID string) (Window, error)
}

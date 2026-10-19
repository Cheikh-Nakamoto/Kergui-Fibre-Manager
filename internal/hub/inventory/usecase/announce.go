package usecase

import (
	"context"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
)

// maxWindowsPerSweep bounds one sweep, so a hub that was down for a while
// catches up over several rounds instead of one long transaction.
const maxWindowsPerSweep = 50

// Announcer publishes a window of new devices. It is a port so the use case
// stays free of the kernel's outbox: the module wires the real one, and a
// test counts what it was asked to announce.
//
// Announce is called inside the transaction that closes the window, which is
// what makes the event and the closing atomic — the household is told once,
// or the window stays open and is retried.
type Announcer interface {
	Announce(ctx context.Context, w domain.Window) error
}

// Windows closes the alert windows whose 2 minutes are up and announces each
// one exactly once.
type Windows struct {
	Repo      domain.Repository
	Announcer Announcer
	Now       Clock
	Log       Logger
}

// Sweep announces every window that is due. It returns how many it closed.
func (uc Windows) Sweep(ctx context.Context) (int, error) {
	now := uc.Now()
	due, err := uc.Repo.DueWindows(ctx, now, maxWindowsPerSweep)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, w := range due {
		if err := uc.Repo.CloseWindow(ctx, w, uc.Now(), uc.Announcer.Announce); err != nil {
			// One household's failed announcement must not stop the others:
			// the window stays open and the next sweep tries again.
			if uc.Log != nil {
				uc.Log.Warn("inventory: announcing new devices", "tenant_id", w.TenantID, "error", err)
			}
			continue
		}
		closed++
	}
	return closed, nil
}

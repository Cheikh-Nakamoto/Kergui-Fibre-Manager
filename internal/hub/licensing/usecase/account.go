package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// The customer area of the website (issue #63) reads what an account
// owns: its payments, and where its 7-day Max trial stands.

// Payments lists the account's payments, newest first (at most 50).
func (s *Service) Payments(ctx context.Context, a domain.AccountID) ([]Payment, error) {
	return s.Store.AccountPayments(ctx, a, 50)
}

// TrialState is where an account's trial stands.
type TrialState string

const (
	// TrialAvailable: never asked for, the number can still have it.
	TrialAvailable TrialState = "available"
	// TrialPending: asked for on the web, waits for the app to bind a box.
	TrialPending TrialState = "pending"
	// TrialRunning: bound to a box, the 7 days are running.
	TrialRunning TrialState = "running"
	// TrialUsed: over, or this number already had one.
	TrialUsed TrialState = "used"
)

// TrialStatus is the account's trial as the customer area shows it.
type TrialStatus struct {
	State       TrialState
	BoxID       domain.BoxID
	RequestedAt time.Time
	StartedAt   time.Time
	EndsAt      time.Time
}

// TrialStatus reports the account's trial.
func (s *Service) TrialStatus(ctx context.Context, a domain.AccountID, phone domain.Phone) (TrialStatus, error) {
	t, err := s.Store.AccountTrial(ctx, a)
	switch {
	case errors.Is(err, ErrNotFound):
		// The number may have had one under another account (a re-created
		// account keeps its number): once per number.
		used, _, err := s.Store.TrialUsed(ctx, phone, "")
		if err != nil {
			return TrialStatus{}, err
		}
		if used {
			return TrialStatus{State: TrialUsed}, nil
		}
		return TrialStatus{State: TrialAvailable}, nil
	case err != nil:
		return TrialStatus{}, err
	}
	st := TrialStatus{BoxID: t.BoxID, RequestedAt: t.RequestedAt, StartedAt: t.StartedAt}
	if t.Pending() {
		st.State = TrialPending
		return st, nil
	}
	st.EndsAt = t.StartedAt.AddDate(0, 0, domain.TrialPass.Days)
	if s.now().Before(st.EndsAt) {
		st.State = TrialRunning
	} else {
		st.State = TrialUsed
	}
	return st, nil
}

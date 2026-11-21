package usecase

import (
	"context"
	"errors"

	"github.com/google/uuid"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// The 7-day Max trial (RFC-006, Passes; decision of 2026-11-21). It is
// asked for on the hub's web page with a phone number alone, never from the
// app, so no box is known yet: the trial waits on the number's account and
// binds to the box serial the first time the app signs in on a box
// (BindBox). The 7 days start at that binding, not at the request: a trial
// asked for and bound weeks later still gives its 7 days.

// RequestTrial records the trial for a number, from the web page. Like a
// purchase it needs no sign-in: the number's account is created if need be,
// and the trial does nothing until that number signs in over WhatsApp. It is
// refused with domain.ErrTrialPhoneUsed when the number already had one (or
// has one pending). A WhatsApp message confirms it; a message that cannot
// go out is logged, the trial stands.
func (s *Service) RequestTrial(ctx context.Context, rawPhone string) (Trial, error) {
	p, err := domain.ParsePhone(rawPhone)
	if err != nil {
		return Trial{}, err
	}
	now := s.now()
	acc, err := s.Store.EnsureAccount(ctx, Account{ID: domain.AccountID(uuid.NewString()), Phone: p, CreatedAt: now})
	if err != nil {
		return Trial{}, err
	}
	t := Trial{Phone: p, Account: acc.ID, RequestedAt: now}
	err = s.Store.InAccount(ctx, acc.ID, func(ctx context.Context) error {
		phoneUsed, _, err := s.Store.TrialUsed(ctx, p, "")
		if err != nil {
			return err
		}
		if err := domain.CheckTrial(phoneUsed, false); err != nil {
			return err
		}
		if err := s.Store.RecordTrial(ctx, t); err != nil {
			if errors.Is(err, ErrTrialRace) {
				return domain.ErrTrialPhoneUsed
			}
			return err
		}
		return nil
	})
	if err != nil {
		return Trial{}, err
	}
	s.info("licensing: trial requested", "account", acc.ID)
	if _, err := s.Sender.Send(ctx, alerts.Phone(p), alerts.OutboundMessage{Text: trialRequestedText}); err != nil {
		s.warn("licensing: trial confirmation not sent", "account", acc.ID, "error", err)
	}
	return t, nil
}

// trialRequestedText goes to the number over WhatsApp, not to the app.
const trialRequestedText = "Kergui : votre essai de 7 jours de Kergui Max est enregistré pour ce numéro. " +
	"Il démarre quand vous vous connectez avec ce numéro dans l'application Kergui, sur votre box."

// TrialOutcome is what binding a box did with the account's trial.
type TrialOutcome string

const (
	// TrialNone: no trial was waiting (none asked for, or already bound).
	TrialNone TrialOutcome = "none"
	// TrialStarted: the waiting trial now runs on this box for 7 days.
	TrialStarted TrialOutcome = "started"
	// TrialBoxUsed: this box already had a trial, so the waiting one does
	// not start here. It keeps waiting for another box.
	TrialBoxUsed TrialOutcome = "box_used"
)

// Binding is the answer to the app's first sign-in on a box.
type Binding struct {
	Subscription domain.Subscription
	Trial        TrialOutcome
}

// BindBox is the app signing in on a box: it binds the account's waiting
// trial to that box and starts its 7 days. Binding always succeeds for a
// valid serial; a box that already had a trial (whoever's) only refuses the
// trial (TrialBoxUsed), and the account stays on what it had there. Calling
// it again is harmless: once bound, nothing is waiting.
func (s *Service) BindBox(ctx context.Context, a domain.AccountID, rawBox string) (Binding, error) {
	b, err := domain.ParseBoxID(rawBox)
	if err != nil {
		return Binding{}, err
	}
	out := Binding{Trial: TrialNone}
	err = s.Store.InAccount(ctx, a, func(ctx context.Context) error {
		now := s.now()
		sub, err := s.subscription(ctx, a, b)
		if err != nil {
			return err
		}
		out.Subscription = sub
		pending, err := s.Store.PendingTrial(ctx, a)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		_, boxUsed, err := s.Store.TrialUsed(ctx, pending.Phone, b)
		if err != nil {
			return err
		}
		// The number's own check was made when the trial was asked for.
		started, err := sub.StartTrial(false, boxUsed, now)
		if err != nil {
			return err
		}
		if err := s.Store.BindTrial(ctx, pending.Phone, b, now); err != nil {
			if errors.Is(err, ErrTrialRace) {
				return domain.ErrTrialBoxUsed
			}
			return err
		}
		if err := s.Store.SaveSubscription(ctx, started); err != nil {
			return err
		}
		out.Subscription, out.Trial = started, TrialStarted
		return nil
	})
	if errors.Is(err, domain.ErrTrialBoxUsed) {
		// The transaction is rolled back: read the box as it stands.
		sub, err := s.subscription(ctx, a, b)
		if err != nil {
			return Binding{}, err
		}
		s.info("licensing: trial refused on a box that had one", "account", a, "box", b)
		return Binding{Subscription: sub, Trial: TrialBoxUsed}, nil
	}
	if err != nil {
		return Binding{}, err
	}
	if out.Trial == TrialStarted {
		s.info("licensing: trial started", "account", a, "box", b, "expires_at", out.Subscription.ExpiresAt)
	}
	return out, nil
}

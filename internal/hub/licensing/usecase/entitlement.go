package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// Entitlement is what a box is entitled to now. Token is empty on Free:
// there is nothing to unlock, and the app needs no token to stay free.
type Entitlement struct {
	Tier   domain.Tier
	Token  string
	Claims domain.Claims
	// Lifetime is a pass with no end (issue #98). The token itself does not
	// say it (its format is fixed by the apps already installed): the app
	// reads it here only to show « à vie » instead of the token's date.
	Lifetime bool
}

// IssueToken signs the entitlement token of the account's pass on a box.
func (s *Service) IssueToken(ctx context.Context, a domain.AccountID, rawBox string) (Entitlement, error) {
	b, err := domain.ParseBoxID(rawBox)
	if err != nil {
		return Entitlement{}, err
	}
	sub, err := s.subscription(ctx, a, b)
	if err != nil {
		return Entitlement{}, err
	}
	now := s.now()
	c, err := domain.IssueClaims(sub, now)
	if errors.Is(err, domain.ErrNoActivePass) {
		return Entitlement{Tier: domain.TierFree}, nil
	}
	if err != nil {
		return Entitlement{}, err
	}
	tok, err := domain.Sign(s.SigningKey, c)
	if err != nil {
		return Entitlement{}, fmt.Errorf("licensing: sign: %w", err)
	}
	return Entitlement{Tier: c.Tier, Token: tok, Claims: c, Lifetime: sub.Lifetime}, nil
}

// Transfer re-binds the account's running pass from one box to another (a
// broken or replaced box), at most domain.MaxTransfers times per
// domain.TransferWindow per account. The limit counts per account, not per
// pass: otherwise moving a pass back and forth between two boxes would
// reset it.
func (s *Service) Transfer(ctx context.Context, a domain.AccountID, rawFrom, rawTo string) (domain.Subscription, error) {
	return s.transfer(ctx, a, rawFrom, rawTo, true)
}

// transfer moves the pass; limited is the customer's own move, checked
// against and counted in the transfer limit.
func (s *Service) transfer(ctx context.Context, a domain.AccountID, rawFrom, rawTo string, limited bool) (domain.Subscription, error) {
	from, err := domain.ParseBoxID(rawFrom)
	if err != nil {
		return domain.Subscription{}, err
	}
	to, err := domain.ParseBoxID(rawTo)
	if err != nil {
		return domain.Subscription{}, err
	}
	var out domain.Subscription
	err = s.Store.InAccount(ctx, a, func(ctx context.Context) error {
		now := s.now()
		sub, err := s.subscription(ctx, a, from)
		if err != nil {
			return err
		}
		var past []time.Time
		if limited {
			if past, err = s.Store.TransfersSince(ctx, a, now.Add(-domain.TransferWindow)); err != nil {
				return err
			}
		}
		if out, err = sub.TransferTo(to, past, now); err != nil {
			return err
		}
		target, err := s.Store.Subscription(ctx, a, to)
		switch {
		case err == nil && target.Active(now):
			return ErrTargetActive
		case err != nil && !errors.Is(err, ErrNotFound):
			return err
		}
		// An expired subscription on the target box is replaced: it holds
		// nothing worth keeping, and the account has one row per box.
		if err := s.Store.DeleteSubscription(ctx, a, to); err != nil {
			return err
		}
		if err := s.Store.DeleteSubscription(ctx, a, from); err != nil {
			return err
		}
		if err := s.Store.SaveSubscription(ctx, out); err != nil {
			return err
		}
		// The Play tokens paying for the pass follow it, so the next
		// renewal lands on the new box.
		if err := s.Store.RebindPlayPurchases(ctx, a, from, to); err != nil {
			return err
		}
		if !limited {
			return nil
		}
		return s.Store.RecordTransfer(ctx, Transfer{Account: a, From: from, To: to, At: now})
	})
	if err != nil {
		return domain.Subscription{}, err
	}
	s.info("licensing: pass transferred", "account", a, "from", from, "to", to, "by_support", !limited)
	return out, nil
}

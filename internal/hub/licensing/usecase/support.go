package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// The back-office's support gestures (issue #78, parent #67): a platform
// administrator finds a customer by number, extends or moves a pass, and
// cuts a lost phone off. They reuse the rules the customer's own gestures
// follow; what support may do that the customer may not (pass the transfer
// limit) is said here, once. None of them sends the customer a sign-in code.

// FindAccount is the account of a number typed in any form (77…, +221…,
// 00221…), parsed as the sign-in parses it. ErrNotFound when the number
// never signed in nor bought anything.
func (s *Service) FindAccount(ctx context.Context, rawPhone string) (Account, error) {
	p, err := domain.ParsePhone(rawPhone)
	if err != nil {
		return Account{}, err
	}
	return s.Store.AccountByPhone(ctx, p)
}

// AccountByID reads an account; ErrNotFound when there is none.
func (s *Service) AccountByID(ctx context.Context, id domain.AccountID) (Account, error) {
	return s.Store.AccountByID(ctx, id)
}

// WaitingPasses lists the account's paid passes that wait for a box.
func (s *Service) WaitingPasses(ctx context.Context, a domain.AccountID) ([]Payment, error) {
	return s.Store.WaitingPasses(ctx, a)
}

// ExtendPass adds days to the pass on one of the account's boxes, on the
// tier it has (domain.Subscription.Extend): a running pass ends later, one
// that ran out runs again from now.
func (s *Service) ExtendPass(ctx context.Context, a domain.AccountID, rawBox string, days int) (domain.Subscription, error) {
	b, err := domain.ParseBoxID(rawBox)
	if err != nil {
		return domain.Subscription{}, err
	}
	var out domain.Subscription
	err = s.Store.InAccount(ctx, a, func(ctx context.Context) error {
		sub, err := s.subscription(ctx, a, b)
		if err != nil {
			return err
		}
		if out, err = sub.Extend(days, s.now()); err != nil {
			return err
		}
		return s.Store.SaveSubscription(ctx, out)
	})
	if err != nil {
		return domain.Subscription{}, err
	}
	s.info("licensing: pass extended by support", "account", a, "box", b, "days", days, "expires_at", out.ExpiresAt)
	return out, nil
}

// AdminTransfer is Transfer for support: the same move, past the
// customer's own limit (a replaced box is a support case), and not counted
// against it.
func (s *Service) AdminTransfer(ctx context.Context, a domain.AccountID, rawFrom, rawTo string) (domain.Subscription, error) {
	return s.transfer(ctx, a, rawFrom, rawTo, false)
}

// RevokeSessions signs every phone of the account out: its licensing
// sessions (kgs_) and, through Identity, its kernel refresh tokens. The
// kernel's access tokens are not stored, so one already issued lives out
// its short TTL; nothing renews it. It returns how many licensing sessions
// it revoked.
func (s *Service) RevokeSessions(ctx context.Context, a domain.AccountID) (int, error) {
	acc, err := s.Store.AccountByID(ctx, a)
	if err != nil {
		return 0, err
	}
	n, err := s.Store.RevokeAccountSessions(ctx, a, s.now())
	if err != nil {
		return 0, err
	}
	if s.Identity != nil {
		if err := s.Identity.SignOut(ctx, acc); err != nil {
			return n, errors.Join(errors.New("licensing: hub sign-out"), err)
		}
	}
	s.info("licensing: sessions revoked by support", "account", a, "sessions", n)
	return n, nil
}

// RenewInput is a pass renewed by hand from the back-office (issue #98):
// tier and days chosen by the admin, and the money received outside
// NabooPay, if any.
type RenewInput struct {
	Account domain.AccountID
	BoxID   string
	Tier    domain.Tier
	Days    int
	// AmountXOF is what the household paid by hand (a Wave transfer,
	// cash); 0 for a gift. Paid, the pass is a ProviderManual payment that
	// counts in the revenue; given, a ProviderSupport one at 0 F as the
	// « Geste commercial » records.
	AmountXOF int64
	// Reference is the payment's id: idempotency of the back-office's
	// call, a fresh one each gesture.
	Reference string
}

// RenewPass renews or extends a box's pass by hand: Days of Tier, added
// from the current end while a pass runs (the same rule as a purchase:
// domain.Subscription.Credit, Basic time converted when Max is chosen), and
// recorded in the payments ledger, so a payment received by hand is revenue
// like any other. A lifetime pass has no end to move: ErrLifetimePass.
func (s *Service) RenewPass(ctx context.Context, in RenewInput) (domain.Subscription, error) {
	b, err := domain.ParseBoxID(in.BoxID)
	if err != nil {
		return domain.Subscription{}, err
	}
	pass, err := domain.ManualPass(in.Tier, in.Days)
	if err != nil {
		return domain.Subscription{}, err
	}
	if in.AmountXOF < 0 || in.Reference == "" {
		return domain.Subscription{}, errors.New("licensing: a manual renewal needs a reference and an amount of 0 or more")
	}
	cur, err := s.subscription(ctx, in.Account, b)
	if err != nil {
		return domain.Subscription{}, err
	}
	if cur.Lifetime {
		return domain.Subscription{}, domain.ErrLifetimePass
	}
	provider := ProviderSupport
	if in.AmountXOF > 0 {
		provider = ProviderManual
	}
	sub, _, err := s.CreditPass(ctx, CreditInput{
		Account: in.Account, BoxID: b, PassCode: pass.Code,
		Provider: provider, Reference: in.Reference, AmountXOF: in.AmountXOF,
	})
	return sub, err
}

// GrantLifetime gives a box a lifetime pass of tier t (issue #98): a friend,
// a partner, a tester. What ran before is kept aside for a revocation.
func (s *Service) GrantLifetime(ctx context.Context, a domain.AccountID, rawBox string, t domain.Tier) (domain.Subscription, error) {
	return s.changeLifetime(ctx, a, rawBox, func(sub domain.Subscription, _ time.Time) (domain.Subscription, error) {
		return sub.GrantLifetime(t)
	})
}

// RevokeLifetime ends a box's lifetime pass: the pass kept aside comes back
// if it still runs, otherwise the box is Free from now.
func (s *Service) RevokeLifetime(ctx context.Context, a domain.AccountID, rawBox string) (domain.Subscription, error) {
	return s.changeLifetime(ctx, a, rawBox, domain.Subscription.RevokeLifetime)
}

func (s *Service) changeLifetime(ctx context.Context, a domain.AccountID, rawBox string,
	change func(domain.Subscription, time.Time) (domain.Subscription, error)) (domain.Subscription, error) {
	b, err := domain.ParseBoxID(rawBox)
	if err != nil {
		return domain.Subscription{}, err
	}
	if _, err := s.Store.AccountByID(ctx, a); err != nil {
		return domain.Subscription{}, err
	}
	var out domain.Subscription
	err = s.Store.InAccount(ctx, a, func(ctx context.Context) error {
		sub, err := s.subscription(ctx, a, b)
		if err != nil {
			return err
		}
		if out, err = change(sub, s.now()); err != nil {
			return err
		}
		return s.Store.SaveSubscription(ctx, out)
	})
	if err != nil {
		return domain.Subscription{}, err
	}
	s.info("licensing: lifetime pass changed by support", "account", a, "box", b, "lifetime", out.Lifetime,
		"tier", out.Tier, "expires_at", out.ExpiresAt)
	return out, nil
}

package usecase

import (
	"context"
	"errors"

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

package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// Entitlement is what a box is entitled to now. Token is empty on Free:
// there is nothing to unlock, and the app needs no token to stay free.
type Entitlement struct {
	Tier   domain.Tier
	Token  string
	Claims domain.Claims
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
	return Entitlement{Tier: c.Tier, Token: tok, Claims: c}, nil
}

// Transfer re-binds the account's running pass from one box to another (a
// broken or replaced box), at most domain.MaxTransfers times per
// domain.TransferWindow per account. The limit counts per account, not per
// pass: otherwise moving a pass back and forth between two boxes would
// reset it.
func (s *Service) Transfer(ctx context.Context, a domain.AccountID, rawFrom, rawTo string) (domain.Subscription, error) {
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
		past, err := s.Store.TransfersSince(ctx, a, now.Add(-domain.TransferWindow))
		if err != nil {
			return err
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
		return s.Store.RecordTransfer(ctx, Transfer{Account: a, From: from, To: to, At: now})
	})
	if err != nil {
		return domain.Subscription{}, err
	}
	s.info("licensing: pass transferred", "account", a, "from", from, "to", to)
	return out, nil
}

// SendReminders sends the WhatsApp reminder of every pass ending within
// domain.ReminderLead, with a NabooPay link to renew the same tier for a
// month. A reminder is claimed before it is sent, so two hub instances do
// not both send it; a send that fails releases the claim for the next
// sweep. It returns how many went out.
func (s *Service) SendReminders(ctx context.Context) (int, error) {
	now := s.now()
	due, err := s.Store.DueReminders(ctx, now, now.Add(domain.ReminderLead), 100)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, sub := range due {
		if !sub.ReminderDue(now) {
			continue
		}
		if err := s.remind(ctx, sub, now); err != nil {
			errs = append(errs, fmt.Errorf("box %s: %w", sub.BoxID, err))
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

func (s *Service) remind(ctx context.Context, sub domain.Subscription, now time.Time) error {
	acc, err := s.Store.AccountByID(ctx, sub.Account)
	if err != nil {
		return err
	}
	ok, err := s.Store.ClaimReminder(ctx, sub, now)
	if err != nil || !ok {
		return err
	}
	link := ""
	if s.Gateway != nil {
		if pass, err := domain.FindPass(sub.Tier, domain.Term1Month); err == nil {
			if res, err := s.openCheckout(ctx, acc, sub.BoxID, pass); err == nil {
				link = res.CheckoutURL
			} else {
				// The reminder still goes out: the app can renew too.
				s.warn("licensing: reminder without a payment link", "box", sub.BoxID, "error", err)
			}
		}
	}
	text := reminderText(sub, link)
	if _, err := s.Sender.Send(ctx, acc.Phone, alerts.OutboundMessage{Text: text}); err != nil {
		if rerr := s.Store.ReleaseReminder(ctx, sub); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}
	s.info("licensing: expiry reminder sent", "account", sub.Account, "box", sub.BoxID, "expires_at", sub.ExpiresAt)
	return nil
}

func reminderText(sub domain.Subscription, link string) string {
	tier := map[domain.Tier]string{domain.TierBasic: "Basic", domain.TierMax: "Max"}[sub.Tier]
	// Dakar is UTC all year, so the date in UTC is the household's date.
	msg := fmt.Sprintf("Kergui : votre pass %s pour la box %s expire le %s.",
		tier, sub.BoxID, sub.ExpiresAt.UTC().Format("02/01/2006 à 15h04"))
	if link != "" {
		return msg + " Renouvelez pour 1 mois ici : " + link
	}
	return msg + " Renouvelez-le depuis l'application Kergui."
}

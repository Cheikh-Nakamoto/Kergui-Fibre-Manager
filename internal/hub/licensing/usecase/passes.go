package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// pendingWindow is how far back the sweep looks for NabooPay orders whose
// webhook never arrived. A checkout nobody paid in two days is abandoned.
const pendingWindow = 48 * time.Hour

// Subscriptions lists the account's passes, one per box.
func (s *Service) Subscriptions(ctx context.Context, a domain.AccountID) ([]domain.Subscription, error) {
	return s.Store.ListSubscriptions(ctx, a)
}

// subscription reads the account's subscription on a box, or a fresh one.
func (s *Service) subscription(ctx context.Context, a domain.AccountID, b domain.BoxID) (domain.Subscription, error) {
	sub, err := s.Store.Subscription(ctx, a, b)
	if errors.Is(err, ErrNotFound) {
		return domain.Subscription{Account: a, BoxID: b, Tier: domain.TierFree}, nil
	}
	return sub, err
}

// StartTrial grants the 7 days of Max, once per number and once per box.
func (s *Service) StartTrial(ctx context.Context, a domain.AccountID, rawBox string) (domain.Subscription, error) {
	b, err := domain.ParseBoxID(rawBox)
	if err != nil {
		return domain.Subscription{}, err
	}
	acc, err := s.Store.AccountByID(ctx, a)
	if err != nil {
		return domain.Subscription{}, err
	}
	var out domain.Subscription
	err = s.Store.InAccount(ctx, a, func(ctx context.Context) error {
		now := s.now()
		phoneUsed, boxUsed, err := s.Store.TrialUsed(ctx, acc.Phone, b)
		if err != nil {
			return err
		}
		sub, err := s.subscription(ctx, a, b)
		if err != nil {
			return err
		}
		if out, err = sub.StartTrial(phoneUsed, boxUsed, now); err != nil {
			return err
		}
		// The trial row's unique constraints are the real guard: another
		// account's request for the same box can race this one.
		if err := s.Store.RecordTrial(ctx, Trial{Phone: acc.Phone, BoxID: b, Account: a, StartedAt: now}); err != nil {
			if errors.Is(err, ErrTrialRace) {
				return domain.ErrTrialBoxUsed
			}
			return err
		}
		return s.Store.SaveSubscription(ctx, out)
	})
	if err != nil {
		return domain.Subscription{}, err
	}
	s.info("licensing: trial started", "account", a, "box", b)
	return out, nil
}

// CheckoutInput is a purchase: the phone number the pass is credited to
// (the app's signed-in account, or what the payer typed on the hub's
// purchase page), the box and the product code.
type CheckoutInput struct {
	Phone    string
	BoxID    string
	PassCode string
}

// CheckoutResult is the hosted NabooPay page to open.
type CheckoutResult struct {
	OrderID     string
	CheckoutURL string
	AmountXOF   int64
	Pass        domain.Pass
	Account     domain.AccountID
}

// Checkout opens a NabooPay checkout for a pass. The account of the number
// is created if it does not exist yet: the purchase page sells to a number
// that may never have signed in, and the pass waits for it there.
func (s *Service) Checkout(ctx context.Context, in CheckoutInput) (CheckoutResult, error) {
	p, err := domain.ParsePhone(in.Phone)
	if err != nil {
		return CheckoutResult{}, err
	}
	b, err := domain.ParseBoxID(in.BoxID)
	if err != nil {
		return CheckoutResult{}, err
	}
	pass, err := domain.PassByCode(strings.TrimSpace(in.PassCode))
	if err != nil {
		return CheckoutResult{}, err
	}
	if s.Gateway == nil {
		return CheckoutResult{}, ErrPaymentsOff
	}
	now := s.now()
	acc, err := s.Store.EnsureAccount(ctx, Account{ID: domain.AccountID(uuid.NewString()), Phone: p, CreatedAt: now})
	if err != nil {
		return CheckoutResult{}, err
	}
	sub, err := s.subscription(ctx, acc.ID, b)
	if err != nil {
		return CheckoutResult{}, err
	}
	if err := sub.CanBuy(pass, now); err != nil {
		return CheckoutResult{}, err
	}
	return s.openCheckout(ctx, acc, b, pass)
}

func (s *Service) openCheckout(ctx context.Context, acc Account, b domain.BoxID, pass domain.Pass) (CheckoutResult, error) {
	res, err := s.Gateway.CreateCheckout(ctx, port.CheckoutRequest{
		Label:       passLabel(pass),
		Description: fmt.Sprintf("Box %s", b),
		AmountXOF:   pass.PriceXOF,
		Phone:       string(acc.Phone),
		SuccessURL:  s.returnURL("success"),
		ErrorURL:    s.returnURL("error"),
	})
	if err != nil {
		return CheckoutResult{}, err
	}
	pay := Payment{
		ID: uuid.NewString(), Provider: ProviderNabooPay, Reference: res.OrderID,
		Account: acc.ID, BoxID: b, PassCode: pass.Code, AmountXOF: res.AmountXOF,
		Status: PaymentPending, CheckoutURL: res.CheckoutURL, CreatedAt: s.now(),
	}
	if err := s.Store.CreatePayment(ctx, pay); err != nil {
		return CheckoutResult{}, err
	}
	s.info("licensing: checkout opened", "account", acc.ID, "box", b, "pass", pass.Code, "order", res.OrderID)
	return CheckoutResult{
		OrderID: res.OrderID, CheckoutURL: res.CheckoutURL, AmountXOF: res.AmountXOF, Pass: pass, Account: acc.ID,
	}, nil
}

func (s *Service) returnURL(status string) string {
	if s.ReturnURL == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(s.ReturnURL, "?") {
		sep = "&"
	}
	return s.ReturnURL + sep + "status=" + url.QueryEscape(status)
}

// passLabel is the product line on NabooPay's page.
func passLabel(p domain.Pass) string {
	tier := map[domain.Tier]string{domain.TierBasic: "Basic", domain.TierMax: "Max"}[p.Tier]
	term := map[domain.Term]string{
		domain.Term1Month: "1 mois", domain.Term3Months: "3 mois", domain.Term12Months: "12 mois",
	}[p.Term]
	return fmt.Sprintf("Kergui %s — %s", tier, term)
}

// CreditInput is one paid pass, whatever the channel: the NabooPay webhook
// or a verified Play purchase.
type CreditInput struct {
	Account domain.AccountID
	BoxID   domain.BoxID
	// PassCode is a catalogue pass ("max_3m"), or for ProviderPlay the Play
	// product id ("kergui_max").
	PassCode  string
	Provider  string
	Reference string // the provider's id of the purchase
	AmountXOF int64
	// ExpiresAt is Play's expiry, required with ProviderPlay: a Play credit
	// sets the expiry to it instead of adding the pass's days.
	ExpiresAt time.Time
}

// credit is how one CreditInput changes a subscription.
type credit struct {
	code    string
	nominal int64
	apply   func(cur domain.Subscription, now time.Time) (domain.Subscription, error)
}

func newCredit(in CreditInput) (credit, error) {
	if in.Provider != ProviderPlay {
		pass, err := domain.PassByCode(in.PassCode)
		if err != nil {
			return credit{}, err
		}
		return credit{code: pass.Code, nominal: pass.PriceXOF, apply: func(cur domain.Subscription, now time.Time) (domain.Subscription, error) {
			return cur.Credit(pass, now)
		}}, nil
	}
	tier, err := domain.PlayProductTier(in.PassCode)
	if err != nil {
		return credit{}, err
	}
	if in.ExpiresAt.IsZero() {
		return credit{}, errors.New("licensing: a Play credit needs Play's expiry")
	}
	return credit{code: strings.TrimSpace(in.PassCode), apply: func(cur domain.Subscription, _ time.Time) (domain.Subscription, error) {
		return cur.ApplyPlay(tier, in.ExpiresAt)
	}}, nil
}

// CreditPass adds a paid pass to the account's subscription on the box,
// exactly once per (Provider, Reference). already is true for a replay.
func (s *Service) CreditPass(ctx context.Context, in CreditInput) (sub domain.Subscription, already bool, err error) {
	c, err := newCredit(in)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	if in.Provider == "" || in.Reference == "" {
		return domain.Subscription{}, false, errors.New("licensing: a credit needs a provider and a reference")
	}
	b, err := domain.ParseBoxID(string(in.BoxID))
	if err != nil {
		return domain.Subscription{}, false, err
	}
	err = s.Store.InAccount(ctx, in.Account, func(ctx context.Context) error {
		now := s.now()
		pay, err := s.Store.PaymentByRef(ctx, in.Provider, in.Reference)
		found := err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if found {
			// A Play payment's box is not compared: the token's binding is
			// the authority on the box, and a transfer moves it.
			sameBox := pay.BoxID == b || in.Provider == ProviderPlay
			if pay.Account != in.Account || !sameBox || pay.PassCode != c.code {
				return ErrPaymentMismatch
			}
			if pay.Status == PaymentCredited {
				already = true
				sub, err = s.subscription(ctx, in.Account, b)
				return err
			}
		}
		cur, err := s.subscription(ctx, in.Account, b)
		if err != nil {
			return err
		}
		if sub, err = c.apply(cur, now); err != nil {
			return err
		}
		if err := s.Store.SaveSubscription(ctx, sub); err != nil {
			return err
		}
		if found {
			return s.Store.SetPaymentStatus(ctx, pay.ID, PaymentCredited, now)
		}
		amount := in.AmountXOF
		if amount == 0 {
			amount = c.nominal
		}
		return s.Store.CreatePayment(ctx, Payment{
			ID: uuid.NewString(), Provider: in.Provider, Reference: in.Reference,
			Account: in.Account, BoxID: b, PassCode: c.code, AmountXOF: amount,
			Status: PaymentCredited, CreatedAt: now, CreditedAt: &now,
		})
	})
	if err != nil {
		return domain.Subscription{}, false, err
	}
	if !already {
		s.info("licensing: pass credited", "account", in.Account, "box", b, "pass", c.code,
			"provider", in.Provider, "expires_at", sub.ExpiresAt)
	}
	return sub, already, nil
}

// SettleOrder acts on a NabooPay order the webhook named. The webhook is
// only a hint: the status is asked from NabooPay's API, as the kergui core
// does (Billing.RefreshByOrder), so a forged or replayed call cannot credit
// anything. credited is true when this call credited the pass.
func (s *Service) SettleOrder(ctx context.Context, orderID string) (credited bool, err error) {
	if orderID == "" || s.Gateway == nil {
		return false, nil
	}
	pay, err := s.Store.PaymentByRef(ctx, ProviderNabooPay, orderID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.settle(ctx, pay)
}

func (s *Service) settle(ctx context.Context, pay Payment) (bool, error) {
	if pay.Status != PaymentPending {
		return false, nil
	}
	st, err := s.Gateway.TransactionStatus(ctx, pay.Reference)
	if err != nil {
		return false, err
	}
	switch st {
	case coredomain.PaymentPaid:
		_, already, err := s.CreditPass(ctx, CreditInput{
			Account: pay.Account, BoxID: pay.BoxID, PassCode: pay.PassCode,
			Provider: pay.Provider, Reference: pay.Reference, AmountXOF: pay.AmountXOF,
		})
		return err == nil && !already, err
	case coredomain.PaymentCancelled:
		return false, s.Store.SetPaymentStatus(ctx, pay.ID, PaymentCancelled, s.now())
	case coredomain.PaymentFailed:
		return false, s.Store.SetPaymentStatus(ctx, pay.ID, PaymentFailed, s.now())
	}
	return false, nil
}

// SettlePending asks NabooPay about the recent orders still pending: the
// safety net for a webhook that never arrived. It returns how many passes
// it credited.
func (s *Service) SettlePending(ctx context.Context) (int, error) {
	if s.Gateway == nil {
		return 0, nil
	}
	list, err := s.Store.PendingPayments(ctx, ProviderNabooPay, s.now().Add(-pendingWindow), 50)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, p := range list {
		ok, err := s.settle(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("order %s: %w", p.Reference, err))
			continue
		}
		if ok {
			n++
		}
	}
	return n, errors.Join(errs...)
}

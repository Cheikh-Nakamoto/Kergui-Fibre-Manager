package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// RTDN notification types (subscriptionNotification.notificationType). The
// hub logs them; what it does is decided by Google's answer, not by these.
const (
	PlayRecovered               = 1
	PlayRenewed                 = 2
	PlayCanceled                = 3
	PlayPurchased               = 4
	PlayOnHold                  = 5
	PlayInGracePeriod           = 6
	PlayRestarted               = 7
	PlayRevoked                 = 12
	PlayExpired                 = 13
	PlayPendingPurchaseCanceled = 20
)

// PlayReference is the payment reference of one Play billing period: the
// purchase token and the expiry Google gave it. Each renewal moves the
// expiry, so each is its own idempotent credit, and the same state told
// twice (a replayed notification, the app's call on every launch) is a
// replay.
func PlayReference(token string, expiresAt time.Time) string {
	return token + "@" + expiresAt.UTC().Format("2006-01-02T15:04:05.000Z")
}

// PlayPurchaseInput is the app reporting a Play purchase.
type PlayPurchaseInput struct {
	Account       domain.AccountID
	BoxID         string
	ProductID     string
	PurchaseToken string
}

// PlayPurchase verifies a purchase token with Google, binds it to the
// account and box, credits the box with Play's expiry and acknowledges the
// purchase. The app calls it after the billing flow and again on launch
// (restoring purchases), so calling it twice is harmless.
func (s *Service) PlayPurchase(ctx context.Context, in PlayPurchaseInput) (domain.Subscription, error) {
	if s.Play == nil {
		return domain.Subscription{}, ErrPlayOff
	}
	b, err := domain.ParseBoxID(in.BoxID)
	if err != nil {
		return domain.Subscription{}, err
	}
	product := strings.TrimSpace(in.ProductID)
	if _, err := domain.PlayProductTier(product); err != nil {
		return domain.Subscription{}, err
	}
	token := strings.TrimSpace(in.PurchaseToken)
	if token == "" {
		return domain.Subscription{}, ErrPlayPurchaseInvalid
	}
	p, err := s.Play.Purchase(ctx, token)
	if err != nil {
		return domain.Subscription{}, err
	}
	li, _, ok := p.Current()
	if !ok || li.ProductID != product {
		return domain.Subscription{}, ErrPlayPurchaseInvalid
	}
	// The app passes the account id to the billing flow; a token bought
	// under another account is someone else's purchase.
	if p.ObfuscatedAccountID != "" && p.ObfuscatedAccountID != string(in.Account) {
		return domain.Subscription{}, ErrPlayTokenBound
	}
	now := s.now()
	switch {
	case p.State == PlayStatePending:
		return domain.Subscription{}, ErrPlayPurchasePending
	case !p.State.Entitled() || !li.ExpiresAt.After(now):
		// A purchase this box already follows (revoked, on hold) ends it
		// here too, so a hub without notifications still catches it when
		// the app reports the purchase again.
		if bound, err := s.Store.PlayBinding(ctx, token); err == nil && bound.Account == in.Account && bound.BoxID == b {
			if _, err := s.applyPlay(ctx, bound, p); err != nil {
				return domain.Subscription{}, err
			}
		}
		return domain.Subscription{}, ErrPlayPurchaseInactive
	}
	bound, err := s.Store.BindPlayPurchase(ctx, PlayBinding{
		Token: token, Account: in.Account, BoxID: b, ProductID: product, BoundAt: now,
	})
	if err != nil {
		return domain.Subscription{}, err
	}
	if bound.Account != in.Account || bound.BoxID != b {
		return domain.Subscription{}, ErrPlayTokenBound
	}
	res, err := s.applyPlay(ctx, bound, p)
	return res.Subscription, err
}

// PlayNotification is a Real-time Developer Notification, as Pub/Sub
// pushed it. Only the token is used: the state is asked from Google.
type PlayNotification struct {
	PackageName   string
	PurchaseToken string
	Type          int
	// Test is the Play Console's "send test notification".
	Test bool
}

// PlayOutcome is what a notification did.
type PlayOutcome string

const (
	// PlayCredited moved the expiry to a new Play billing period.
	PlayCredited PlayOutcome = "credited"
	// PlayUnchanged was a state the hub already had (a replay, a cancel
	// that runs to its expiry).
	PlayUnchanged PlayOutcome = "unchanged"
	// PlayEnded stopped the box's subscription now.
	PlayEnded PlayOutcome = "ended"
	// PlayIgnored concerned nothing the hub tracks: a test, another app, a
	// token no account claimed yet, or one a newer purchase replaced.
	PlayIgnored PlayOutcome = "ignored"
)

// PlaySync is the result of following a purchase's state.
type PlaySync struct {
	Outcome      PlayOutcome
	Subscription domain.Subscription
}

// PlayNotify acts on a notification. It never trusts the body: it asks
// Google for the purchase and follows its state. An error means Google or
// the database could not be reached, and the push should be retried.
func (s *Service) PlayNotify(ctx context.Context, n PlayNotification) (PlaySync, error) {
	if s.Play == nil {
		return PlaySync{}, ErrPlayOff
	}
	token := strings.TrimSpace(n.PurchaseToken)
	if n.Test || token == "" || (s.PlayPackage != "" && n.PackageName != s.PlayPackage) {
		return PlaySync{Outcome: PlayIgnored}, nil
	}
	p, err := s.Play.Purchase(ctx, token)
	if errors.Is(err, ErrPlayPurchaseInvalid) {
		return PlaySync{Outcome: PlayIgnored}, nil
	}
	if err != nil {
		return PlaySync{}, err
	}
	bound, err := s.Store.PlayBinding(ctx, token)
	if errors.Is(err, ErrNotFound) {
		bound, err = s.bindLinked(ctx, token, p)
	}
	if errors.Is(err, ErrNotFound) {
		// No account claimed it yet: the app's call binds it.
		return PlaySync{Outcome: PlayIgnored}, nil
	}
	if err != nil {
		return PlaySync{}, err
	}
	res, err := s.applyPlay(ctx, bound, p)
	if err == nil {
		s.info("licensing: play notification", "type", n.Type, "account", bound.Account, "box", bound.BoxID,
			"outcome", res.Outcome, "expires_at", res.Subscription.ExpiresAt)
	}
	return res, err
}

// bindLinked binds a new token (an upgrade, a downgrade, a re-subscription)
// to the account and box of the token it replaces, so the box follows it
// even when its notification arrives before the app's call.
func (s *Service) bindLinked(ctx context.Context, token string, p PlayPurchase) (PlayBinding, error) {
	li, _, ok := p.Current()
	if p.LinkedPurchaseToken == "" || !ok {
		return PlayBinding{}, ErrNotFound
	}
	old, err := s.Store.PlayBinding(ctx, p.LinkedPurchaseToken)
	if err != nil {
		return PlayBinding{}, err
	}
	return s.Store.BindPlayPurchase(ctx, PlayBinding{
		Token: token, Account: old.Account, BoxID: old.BoxID, ProductID: li.ProductID, BoundAt: s.now(),
	})
}

// applyPlay makes the box follow the purchase's state: credited up to
// Play's expiry while it grants access, ended now once it does not.
func (s *Service) applyPlay(ctx context.Context, bound PlayBinding, p PlayPurchase) (PlaySync, error) {
	a, b := bound.Account, bound.BoxID
	// Only the token bound last drives the box: an old token's "expired",
	// after an upgrade replaced it, must not end the new subscription.
	latest, err := s.Store.LatestPlayBinding(ctx, a, b)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlaySync{}, err
	}
	if err == nil && latest.Token != bound.Token {
		sub, err := s.subscription(ctx, a, b)
		return PlaySync{Outcome: PlayIgnored, Subscription: sub}, err
	}
	li, tier, ok := p.Current()
	if !ok {
		sub, err := s.subscription(ctx, a, b)
		return PlaySync{Outcome: PlayIgnored, Subscription: sub}, err
	}
	now := s.now()
	switch {
	case p.State.Entitled() && li.ExpiresAt.After(now):
		sub, already, err := s.CreditPass(ctx, CreditInput{
			Account: a, BoxID: b, PassCode: li.ProductID, Provider: ProviderPlay,
			Reference: PlayReference(bound.Token, li.ExpiresAt), AmountXOF: playAmount(li, tier),
			ExpiresAt: li.ExpiresAt,
		})
		if err != nil {
			return PlaySync{}, err
		}
		if li.FreeTrial {
			s.recordPlayTrial(ctx, a, b, now)
		}
		// Google refunds a purchase not acknowledged within three days.
		// A failure is returned so the app (or Pub/Sub) calls again; the
		// credit above is then a replay.
		if !p.Acknowledged {
			if err := s.Play.Acknowledge(ctx, li.ProductID, bound.Token); err != nil {
				return PlaySync{}, fmt.Errorf("licensing: acknowledge: %w", err)
			}
		}
		out := PlaySync{Outcome: PlayCredited, Subscription: sub}
		if already {
			out.Outcome = PlayUnchanged
		}
		return out, nil
	case p.State == PlayStatePending || p.State == PlayStatePendingPurchaseCanceled || p.State == "":
		sub, err := s.subscription(ctx, a, b)
		return PlaySync{Outcome: PlayIgnored, Subscription: sub}, err
	default: // expired (revoked included), on hold, paused, or past its expiry
		return s.endPlay(ctx, a, b, now)
	}
}

func (s *Service) endPlay(ctx context.Context, a domain.AccountID, b domain.BoxID, now time.Time) (PlaySync, error) {
	out := PlaySync{Outcome: PlayUnchanged}
	err := s.Store.InAccount(ctx, a, func(ctx context.Context) error {
		cur, err := s.subscription(ctx, a, b)
		if err != nil {
			return err
		}
		out.Subscription = cur.EndPlay(now)
		if out.Subscription == cur {
			return nil
		}
		out.Outcome = PlayEnded
		return s.Store.SaveSubscription(ctx, out.Subscription)
	})
	if err != nil {
		return PlaySync{}, err
	}
	return out, nil
}

// recordPlayTrial records a Play free trial against the number and the box,
// so the hub's own trial is not granted on top. Play decides eligibility
// for its trial by Google account, so the hub only records it, and only
// when neither the number nor the box had a trial (the trials table holds
// one row per pair). A failure is logged: the purchase itself went through.
func (s *Service) recordPlayTrial(ctx context.Context, a domain.AccountID, b domain.BoxID, now time.Time) {
	acc, err := s.Store.AccountByID(ctx, a)
	if err == nil {
		var phoneUsed, boxUsed bool
		if phoneUsed, boxUsed, err = s.Store.TrialUsed(ctx, acc.Phone, b); err == nil && !phoneUsed && !boxUsed {
			err = s.Store.RecordTrial(ctx, Trial{Phone: acc.Phone, BoxID: b, Account: a, RequestedAt: now, StartedAt: now})
		}
	}
	if err != nil && !errors.Is(err, ErrTrialRace) {
		s.warn("licensing: play trial not recorded", "account", a, "box", b, "error", err)
	}
}

// playAmount is what the period cost in francs CFA: Play's price when it is
// in XOF, else the tier's monthly NabooPay price as the nominal value.
func playAmount(li PlayLineItem, tier domain.Tier) int64 {
	if li.Currency == "XOF" && li.PriceUnits > 0 {
		return li.PriceUnits
	}
	if p, err := domain.FindPass(tier, domain.Term1Month); err == nil {
		return p.PriceXOF
	}
	return 0
}

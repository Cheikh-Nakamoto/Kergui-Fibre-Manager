package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// Errors of the Google Play channel.
var (
	// ErrPlayOff is a Play call on a hub with no Play configuration.
	ErrPlayOff = errors.New("licensing: Google Play Billing is not configured on this hub")
	// ErrPlayUnavailable is Google's API failing or refusing the hub's
	// credentials: try again later.
	ErrPlayUnavailable = errors.New("licensing: Google Play is unavailable")
	// ErrPlayPurchaseInvalid is a purchase token Google does not know for
	// this app, or one for another product than the request named.
	ErrPlayPurchaseInvalid = errors.New("licensing: Google Play does not know this purchase")
	// ErrPlayPurchasePending is a purchase not paid yet (a pending payment
	// method): the app retries once Play says it went through.
	ErrPlayPurchasePending = errors.New("licensing: this Google Play purchase is not paid yet")
	// ErrPlayPurchaseInactive is a subscription that grants nothing now:
	// expired, on hold, paused.
	ErrPlayPurchaseInactive = errors.New("licensing: this Google Play subscription is not active")
	// ErrPlayTokenBound refuses a purchase token already bound to another
	// account, or to another box of this account (moving it is a transfer).
	ErrPlayTokenBound = errors.New("licensing: this Google Play purchase belongs to another account or box")
	// ErrPushUnauthenticated is a Pub/Sub push whose OIDC token does not
	// check out. Any other error from a PushVerifier is transient.
	ErrPushUnauthenticated = errors.New("licensing: the push is not signed by the configured service account")
)

// PushVerifier authenticates a Pub/Sub push from its bearer token.
type PushVerifier interface {
	Verify(ctx context.Context, bearer string) error
}

// PlayState is subscriptionsv2's subscriptionState.
type PlayState string

const (
	PlayStateActive                  PlayState = "SUBSCRIPTION_STATE_ACTIVE"
	PlayStateCanceled                PlayState = "SUBSCRIPTION_STATE_CANCELED"
	PlayStateInGracePeriod           PlayState = "SUBSCRIPTION_STATE_IN_GRACE_PERIOD"
	PlayStateOnHold                  PlayState = "SUBSCRIPTION_STATE_ON_HOLD"
	PlayStatePaused                  PlayState = "SUBSCRIPTION_STATE_PAUSED"
	PlayStateExpired                 PlayState = "SUBSCRIPTION_STATE_EXPIRED"
	PlayStatePending                 PlayState = "SUBSCRIPTION_STATE_PENDING"
	PlayStatePendingPurchaseCanceled PlayState = "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"
)

// Entitled reports whether the state grants access until the expiry. A
// canceled subscription (auto-renew turned off) runs to its expiry; one in
// its grace period keeps access while Google retries the payment.
func (s PlayState) Entitled() bool {
	return s == PlayStateActive || s == PlayStateCanceled || s == PlayStateInGracePeriod
}

// PlayPurchase is what Google says about a subscription purchase token.
type PlayPurchase struct {
	State        PlayState
	Acknowledged bool
	// LinkedPurchaseToken is the token this one replaces (an upgrade, a
	// downgrade, a re-subscription).
	LinkedPurchaseToken string
	// ObfuscatedAccountID is what the app passed to the billing flow as
	// setObfuscatedAccountId: the hub account id, when the app sets it.
	ObfuscatedAccountID string
	LineItems           []PlayLineItem
}

// PlayLineItem is one product of the purchase.
type PlayLineItem struct {
	ProductID string
	ExpiresAt time.Time
	// FreeTrial is true while the line item is in a free-trial offer phase.
	FreeTrial bool
	// PriceUnits is the recurring price in whole units of Currency.
	PriceUnits int64
	Currency   string
}

// Current is the line item of a product this hub sells that runs the
// longest (a tie goes to the higher tier), with its tier. false when the
// purchase holds none of ours.
func (p PlayPurchase) Current() (PlayLineItem, domain.Tier, bool) {
	var (
		best     PlayLineItem
		bestTier domain.Tier
		found    bool
	)
	for _, li := range p.LineItems {
		t, err := domain.PlayProductTier(li.ProductID)
		if err != nil {
			continue
		}
		if !found || li.ExpiresAt.After(best.ExpiresAt) ||
			(li.ExpiresAt.Equal(best.ExpiresAt) && t.Includes(bestTier) && t != bestTier) {
			best, bestTier, found = li, t, true
		}
	}
	return best, bestTier, found
}

// PlayVerifier is the Google Play Developer API: it answers for a purchase
// token of the hub's app, and acknowledges it. Purchase returns
// ErrPlayPurchaseInvalid for a token Google does not know, and
// ErrPlayUnavailable when Google cannot be asked.
type PlayVerifier interface {
	Purchase(ctx context.Context, token string) (PlayPurchase, error)
	Acknowledge(ctx context.Context, productID, token string) error
}

// PlayBinding ties a purchase token to the account and box it pays for.
// A token is bound once; a transfer moves the box.
type PlayBinding struct {
	Token     string
	Account   domain.AccountID
	BoxID     domain.BoxID
	ProductID string
	BoundAt   time.Time
}

// PlayStore is the storage the Play channel adds to Store.
type PlayStore interface {
	// BindPlayPurchase binds a token unless it is bound already, and
	// returns the binding that holds (the existing one, if any).
	BindPlayPurchase(ctx context.Context, b PlayBinding) (PlayBinding, error)
	// PlayBinding returns ErrNotFound for a token never bound.
	PlayBinding(ctx context.Context, token string) (PlayBinding, error)
	// LatestPlayBinding is the token bound last to the account's box: the
	// one whose state the box follows. ErrNotFound when there is none.
	LatestPlayBinding(ctx context.Context, a domain.AccountID, b domain.BoxID) (PlayBinding, error)
	// RebindPlayPurchases moves the account's tokens from one box to
	// another, with the subscription they pay for.
	RebindPlayPurchases(ctx context.Context, a domain.AccountID, from, to domain.BoxID) error
}

package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A lifetime pass (issue #98) is a tier with no end, granted from the
// back-office to a friend, a partner or a tester, and revocable there.
//
// It is stored as an ordinary subscription whose ExpiresAt is
// LifetimeExpiresAt, flagged Lifetime. Keeping a real (far) date rather than
// a NULL is deliberate: every reader that asks « does a pass run? » with
// expires_at > now (the token, the messages' audience, the health counts,
// the households list…) keeps working unchanged, and the entitlement token
// is still capped at TokenMaxLifetime and refreshed by the app as for any
// long pass, so the apps already installed verify it as they always did.
//
// The flag says what the date alone would not: no expiry reminder, no place
// in the expiring list or the renewal cohorts, no « jusqu'au 31/12/9999 »
// on screen. The pass that ran before the grant is kept aside (PreLifetime*)
// and comes back when the lifetime pass is revoked; money paid meanwhile is
// added to it, never lost.

// LifetimeExpiresAt is the end a lifetime pass carries: the last day Go's
// JSON encoding and JavaScript's Date both read as a date.
var LifetimeExpiresAt = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

var (
	// ErrLifetimePass refuses changing the end of a lifetime pass: it has
	// none to move. Revoke it first.
	ErrLifetimePass = errors.New("licensing: this box has a lifetime pass")
	// ErrNotLifetime is a revocation on a box that has no lifetime pass.
	ErrNotLifetime = errors.New("licensing: this box has no lifetime pass")
	// ErrManualPass is a manual renewal outside 1..MaxExtendDays days or on
	// a tier that is not paid.
	ErrManualPass = errors.New("licensing: a manual pass is Basic or Max for 1 to 365 days")
)

// underlying is the pass that ran before the lifetime grant, as a
// subscription of its own, so the usual rules can add to it.
func (s Subscription) underlying() Subscription {
	u := s
	u.Lifetime, u.PreLifetimeTier, u.PreLifetimeExpiresAt = false, "", time.Time{}
	u.Tier, u.ExpiresAt = s.PreLifetimeTier, s.PreLifetimeExpiresAt
	if u.Tier == "" {
		u.Tier = TierFree
	}
	return u
}

// withUnderlying stores u as the pass kept aside under the lifetime one.
func (s Subscription) withUnderlying(u Subscription) Subscription {
	s.PreLifetimeTier, s.PreLifetimeExpiresAt = u.Tier, u.ExpiresAt
	return s
}

// GrantLifetime makes the box's pass a lifetime pass of tier t. The pass
// running before is kept aside for a revocation. Granting again on a
// lifetime pass only changes its tier.
func (s Subscription) GrantLifetime(t Tier) (Subscription, error) {
	if !t.Includes(TierBasic) {
		return s, ErrUnknownPass
	}
	if !s.Lifetime {
		tier := s.Tier
		if tier == "" {
			tier = TierFree
		}
		s.PreLifetimeTier, s.PreLifetimeExpiresAt = tier, s.ExpiresAt
	}
	s.Lifetime, s.Tier, s.ExpiresAt = true, t, LifetimeExpiresAt
	return s, nil
}

// RevokeLifetime ends a lifetime pass. The pass kept aside comes back if it
// still runs at now (with any time paid meanwhile); otherwise the box ends
// its paid tier now and is Free.
func (s Subscription) RevokeLifetime(now time.Time) (Subscription, error) {
	if !s.Lifetime {
		return s, ErrNotLifetime
	}
	u := s.underlying()
	if !u.Active(now) {
		// The tier stays as the one that last ran, ending now.
		u.Tier, u.ExpiresAt = s.Tier, now
	}
	return u, nil
}

// ManualPass is a pass of tier t for days days, renewed by hand from the
// back-office (issue #98): a payment received outside NabooPay, or a gift
// of a duration the catalogue does not sell. Its code, "<tier>_<days>d"
// (max_30d), is not a catalogue code, so no checkout can name it.
func ManualPass(t Tier, days int) (Pass, error) {
	if !t.Includes(TierBasic) || days < 1 || days > MaxExtendDays {
		return Pass{}, ErrManualPass
	}
	return Pass{Code: fmt.Sprintf("%s_%dd", t, days), Tier: t, Days: days}, nil
}

// ManualPassByCode reads a ManualPass code back.
func ManualPassByCode(code string) (Pass, error) {
	tier, rest, ok := strings.Cut(code, "_")
	days, found := strings.CutSuffix(rest, "d")
	if !ok || !found {
		return Pass{}, ErrUnknownPass
	}
	n, err := strconv.Atoi(days)
	if err != nil || strconv.Itoa(n) != days {
		return Pass{}, ErrUnknownPass
	}
	p, err := ManualPass(Tier(tier), n)
	if err != nil {
		return Pass{}, ErrUnknownPass
	}
	return p, nil
}

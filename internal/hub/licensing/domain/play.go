package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Channel is how a subscription is paid (RFC-006, Two channels). It decides
// who moves the expiry: the hub, adding a prepaid pass's days, or Google
// Play, whose auto-renewing subscription carries its own expiry.
type Channel string

const (
	// ChannelPrepaid is a NabooPay pass or the hub's trial. It is also what
	// an empty channel means: every subscription before Play was prepaid.
	ChannelPrepaid Channel = "prepaid"
	// ChannelPlay is a Google Play Billing subscription.
	ChannelPlay Channel = "play"
)

// ParseChannel reads a stored channel; empty is prepaid.
func ParseChannel(s string) (Channel, error) {
	switch c := Channel(strings.TrimSpace(s)); c {
	case "", ChannelPrepaid:
		return ChannelPrepaid, nil
	case ChannelPlay:
		return ChannelPlay, nil
	default:
		return "", fmt.Errorf("licensing: unknown channel %q", s)
	}
}

// ErrUnknownProduct is a Play product id this hub does not sell.
var ErrUnknownProduct = errors.New("licensing: unknown Play product")

// playProducts are the Play Console subscription ids, one monthly base plan
// each. The trial is an offer configured on them in the Play Console.
var playProducts = map[string]Tier{
	"kergui_basic": TierBasic,
	"kergui_max":   TierMax,
}

// PlayProductTier is the tier a Play subscription id unlocks. Ids are
// matched exactly (after trimming): Play's ids are case-sensitive.
func PlayProductTier(productID string) (Tier, error) {
	t, ok := playProducts[strings.TrimSpace(productID)]
	if !ok {
		return "", ErrUnknownProduct
	}
	return t, nil
}

// PlayProduct is the Play subscription id of a paid tier, "" for Free.
func PlayProduct(t Tier) string {
	for id, pt := range playProducts {
		if pt == t {
			return id
		}
	}
	return ""
}

// ApplyPlay sets the subscription to what Google Play says: the tier of the
// product and Play's own expiry, not days added to the current one. Google
// is the authority on a Play-billed box: renewals, grace periods, upgrades
// and refunds are all its decision, and re-reading its state is how the hub
// follows them. Any prepaid time still running on the box is replaced; the
// two channels ship in separate app flavors, so a box mixing both is a
// support case.
func (s Subscription) ApplyPlay(t Tier, expiresAt time.Time) (Subscription, error) {
	if !t.Includes(TierBasic) {
		return s, ErrUnknownProduct
	}
	if expiresAt.IsZero() {
		return s, errors.New("licensing: a Play subscription needs an expiry")
	}
	if s.Lifetime {
		// Play keeps billing a box that was then given a lifetime pass: its
		// state goes to the pass kept aside, back if the lifetime one ends.
		u, err := s.underlying().ApplyPlay(t, expiresAt)
		if err != nil {
			return s, err
		}
		s.Channel = ChannelPlay
		return s.withUnderlying(u), nil
	}
	s.Tier, s.ExpiresAt, s.Channel = t, expiresAt.UTC(), ChannelPlay
	return s, nil
}

// EndPlay stops a Play subscription at now (expired, on hold, paused or
// revoked). A prepaid subscription is left alone: Play has no say on it.
func (s Subscription) EndPlay(now time.Time) Subscription {
	if s.Lifetime {
		// Play's end ends the pass kept aside, never the lifetime one.
		if s.Channel == ChannelPlay && s.PreLifetimeExpiresAt.After(now) {
			s.PreLifetimeExpiresAt = now
		}
		return s
	}
	if s.Channel == ChannelPlay && s.ExpiresAt.After(now) {
		s.ExpiresAt = now
	}
	return s
}

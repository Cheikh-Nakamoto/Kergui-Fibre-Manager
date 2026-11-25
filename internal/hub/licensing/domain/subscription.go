package domain

import (
	"errors"
	"time"
)

var (
	// ErrMaxActive refuses selling a Basic pass while Max runs: it would
	// either waste the Max days or need a queue of pending passes.
	ErrMaxActive = errors.New("licensing: a Max pass is active; buy Max to extend it")
	// ErrNoActivePass is a transfer of a subscription that has nothing left.
	ErrNoActivePass = errors.New("licensing: no active pass on this box")
	// ErrSameBox is a transfer to the box the pass is already on.
	ErrSameBox = errors.New("licensing: the pass is already on this box")
	// ErrTransferLimit says the owner used up the self-service transfers;
	// beyond that it goes through support (RFC-006, Accounts).
	ErrTransferLimit = errors.New("licensing: 2 box transfers already in the last 30 days; contact support to move the pass again")
	// ErrTrialPhoneUsed and ErrTrialBoxUsed say who already had the trial.
	ErrTrialPhoneUsed = errors.New("licensing: this number already had its trial")
	ErrTrialBoxUsed   = errors.New("licensing: this box already had its trial")
	// ErrExtendDays is an extension outside 1..MaxExtendDays days.
	ErrExtendDays = errors.New("licensing: an extension is 1 to 365 days")
)

const (
	// MaxTransfers is how many times a pass may move to another box per
	// TransferWindow (rolling, not calendar).
	MaxTransfers   = 2
	TransferWindow = 30 * 24 * time.Hour
	// ReminderLead is how long before expiry the first renewal reminder
	// (J-3) goes out (reminder.go).
	ReminderLead = 3 * 24 * time.Hour
	// MaxExtendDays caps one support extension (issue #78).
	MaxExtendDays = 365
)

// The pro-rata rate between Basic and Max time is the monthly price ratio,
// 650 → 2 000 (RFC-006, Upgrade). It is the same for every term: a Basic
// day is worth 650/2000 of a Max day whichever pass it came from.
const (
	basicRate = 650
	maxRate   = 2000
)

// Subscription is what one account holds on one box. Tier is the tier of
// the pass running (or that last ran); only one tier runs at a time.
type Subscription struct {
	Account   AccountID
	BoxID     BoxID
	Tier      Tier
	ExpiresAt time.Time
	// Channel is how the subscription is paid; empty means prepaid. A Play
	// subscription renews by itself, so it gets no expiry reminder.
	Channel Channel
	// Lifetime is a pass with no end (lifetime.go): ExpiresAt is then
	// LifetimeExpiresAt, and PreLifetimeTier and PreLifetimeExpiresAt are the
	// pass that ran before the grant, back if it is revoked.
	Lifetime             bool
	PreLifetimeTier      Tier
	PreLifetimeExpiresAt time.Time
}

// Active reports whether a paid (or trial) tier runs at now.
func (s Subscription) Active(now time.Time) bool {
	return s.Tier.Includes(TierBasic) && now.Before(s.ExpiresAt)
}

// EffectiveTier is what the box is entitled to at now: Free once the pass
// has run out. Rules already written to the box stay; paid screens freeze.
func (s Subscription) EffectiveTier(now time.Time) Tier {
	if !s.Active(now) {
		return TierFree
	}
	return s.Tier
}

// ConvertBasicToMax turns remaining Basic time into Max time at 650/2000,
// rounded down to the whole hour: hours keep a short Basic remainder worth
// something (days would turn 3 Basic days into none), and rounding down
// means converting never creates time. It works in seconds so a year's
// worth does not overflow.
func ConvertBasicToMax(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	secs := int64(d/time.Second) * basicRate / maxRate
	return (time.Duration(secs) * time.Second).Truncate(time.Hour)
}

// validPass is a pass that adds paid-tier time.
func validPass(p Pass) bool { return p.Tier.Includes(TierBasic) && p.Days > 0 }

// CanBuy says whether a checkout may be opened for p. Only catalogue passes
// are sold, and Basic is not sold while Max runs.
func (s Subscription) CanBuy(p Pass, now time.Time) error {
	if c, err := PassByCode(p.Code); err != nil || c != p {
		return ErrUnknownPass
	}
	if s.Lifetime {
		// Nothing to buy: the box already has every day it can have.
		return ErrLifetimePass
	}
	if p.Tier == TierBasic && s.EffectiveTier(now) == TierMax {
		return ErrMaxActive
	}
	return nil
}

// Credit adds a paid pass (or the trial) to the subscription.
//
//   - Same tier, or nothing running: the pass extends from max(now, expiry).
//   - Max on a running Basic: the remaining Basic time is converted into Max
//     time (ConvertBasicToMax), then the Max pass is added.
//   - Basic on a running Max: CanBuy refuses to sell it, but a payment that
//     arrives anyway (two checkouts, a webhook replay of an old one) has been
//     paid for, so it is honoured the other way round: its days are converted
//     into Max time at the same rate and added to the Max pass.
func (s Subscription) Credit(p Pass, now time.Time) (Subscription, error) {
	if !validPass(p) {
		return s, ErrUnknownPass
	}
	if s.Lifetime {
		// Paid while a lifetime pass runs (CanBuy refuses it, but a payment
		// may still arrive): the money is not lost, its days go to the pass
		// kept aside, which comes back if the lifetime pass is revoked.
		u, err := s.underlying().Credit(p, now)
		if err != nil {
			return s, err
		}
		return s.withUnderlying(u), nil
	}
	s.Channel = ChannelPrepaid
	if !s.Active(now) {
		s.Tier, s.ExpiresAt = p.Tier, now.Add(p.Duration())
		return s, nil
	}
	switch {
	case s.Tier == p.Tier:
		s.ExpiresAt = s.ExpiresAt.Add(p.Duration())
	case p.Tier == TierMax: // upgrade
		s.Tier = TierMax
		s.ExpiresAt = now.Add(ConvertBasicToMax(s.ExpiresAt.Sub(now)) + p.Duration())
	default: // Basic paid while Max runs
		s.ExpiresAt = s.ExpiresAt.Add(ConvertBasicToMax(p.Duration()))
	}
	return s, nil
}

// CheckTrial says whether the 7-day Max trial may start: once per phone
// number and once per box (RFC-006, Passes). The number is checked first,
// so the refusal names what the person can change least.
func CheckTrial(phoneTrialled, boxTrialled bool) error {
	switch {
	case phoneTrialled:
		return ErrTrialPhoneUsed
	case boxTrialled:
		return ErrTrialBoxUsed
	}
	return nil
}

// StartTrial credits TrialPass if CheckTrial allows it.
func (s Subscription) StartTrial(phoneTrialled, boxTrialled bool, now time.Time) (Subscription, error) {
	if err := CheckTrial(phoneTrialled, boxTrialled); err != nil {
		return s, err
	}
	return s.Credit(TrialPass, now)
}

// CheckTransfer says whether one more box transfer is allowed, given when the
// past ones happened. A transfer counts while it is less than TransferWindow
// old; one stamped after now (clock skew between hub instances) counts too.
func CheckTransfer(past []time.Time, now time.Time) error {
	n := 0
	for _, t := range past {
		if now.Sub(t) < TransferWindow {
			n++
		}
	}
	if n >= MaxTransfers {
		return ErrTransferLimit
	}
	return nil
}

// TransferTo re-binds a running pass to another box (a broken or replaced
// one). The tier and expiry move unchanged.
func (s Subscription) TransferTo(box BoxID, past []time.Time, now time.Time) (Subscription, error) {
	switch {
	case box == "":
		return s, ErrInvalidBoxID
	case box == s.BoxID:
		return s, ErrSameBox
	case !s.Active(now):
		return s, ErrNoActivePass
	}
	if err := CheckTransfer(past, now); err != nil {
		return s, err
	}
	s.BoxID = box
	return s, nil
}

// Extend adds days to the box's pass on the tier it has: a support gesture
// (issue #78), compensating an outage. A running pass ends days later; one
// that ran out runs again from now. A box that never had a paid tier has
// nothing to extend: support grants it a pass instead.
func (s Subscription) Extend(days int, now time.Time) (Subscription, error) {
	if days < 1 || days > MaxExtendDays {
		return s, ErrExtendDays
	}
	if s.Lifetime {
		return s, ErrLifetimePass
	}
	if !s.Tier.Includes(TierBasic) {
		return s, ErrNoActivePass
	}
	from := s.ExpiresAt
	if !s.Active(now) {
		from = now
	}
	s.ExpiresAt = from.AddDate(0, 0, days)
	return s, nil
}

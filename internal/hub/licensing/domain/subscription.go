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
)

const (
	// MaxTransfers is how many times a pass may move to another box per
	// TransferWindow (rolling, not calendar).
	MaxTransfers   = 2
	TransferWindow = 30 * 24 * time.Hour
	// ReminderLead is how long before expiry the WhatsApp reminder goes out.
	ReminderLead = 3 * 24 * time.Hour
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
	// ReminderSentFor is the expiry the last reminder announced. Crediting a
	// pass moves the expiry, which is what makes the next reminder due.
	ReminderSentFor time.Time
	// Channel is how the subscription is paid; empty means prepaid. A Play
	// subscription renews by itself, so it gets no expiry reminder.
	Channel Channel
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

// ReminderDue reports whether the expiry reminder should go out: a prepaid
// pass ends within ReminderLead and no reminder announced this expiry yet.
// A Play subscription renews by itself (and the play flavor must not carry a
// NabooPay link), so it never gets one.
func (s Subscription) ReminderDue(now time.Time) bool {
	return s.Channel != ChannelPlay && s.Active(now) && s.ExpiresAt.Sub(now) <= ReminderLead && !s.ReminderSentFor.Equal(s.ExpiresAt)
}

// ReminderSent records that the reminder for the current expiry went out.
func (s Subscription) ReminderSent() Subscription {
	s.ReminderSentFor = s.ExpiresAt
	return s
}

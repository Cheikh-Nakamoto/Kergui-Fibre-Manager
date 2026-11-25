package domain

import "time"

// Renewal reminders (issue #99). A prepaid pass gets two reminders for each
// end date: three days before it (J-3), then on the day it ends (J0). Each
// goes out once per end date and step: a pass that gets more time has a new
// end date, which is what makes the next reminders due. A lifetime pass has
// no end, and a Play subscription renews by itself: neither gets any.

// ReminderStep is one of the two reminders of an end date.
type ReminderStep string

const (
	// ReminderBefore is the reminder three days before the end (J-3).
	ReminderBefore ReminderStep = "j3"
	// ReminderDay is the reminder on the day the pass ends (J0), in Dakar.
	ReminderDay ReminderStep = "j0"
)

// Dakar is the households' time zone. Senegal keeps UTC all year, with no
// summer time, so a fixed zone says the same as Africa/Dakar without the
// hub image having to ship the time zone database.
var Dakar = time.FixedZone("Africa/Dakar", 0)

const (
	// ReminderFromHour and ReminderToHour bound, in Dakar, the hours a
	// reminder may go out: a phone that buzzes at 3 a.m. for a pass is a
	// customer lost, not won. A reminder due at night waits for the morning.
	ReminderFromHour = 9
	ReminderToHour   = 20
	// ReminderTries is how many times a WhatsApp reminder is tried before
	// the hub gives up on it (WhatsApp unlinked or down for a while).
	ReminderTries = 3
)

// ReminderHours reports whether now is within the hours reminders go out.
func ReminderHours(now time.Time) bool {
	h := now.In(Dakar).Hour()
	return h >= ReminderFromHour && h < ReminderToHour
}

// DayStart is the start of t's day in Dakar.
func DayStart(t time.Time) time.Time {
	d := t.In(Dakar)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, Dakar).UTC()
}

// Remindable reports whether the pass gets renewal reminders at all: a paid
// tier (the trial included), prepaid, with an end.
func (s Subscription) Remindable() bool {
	return s.Tier.Includes(TierBasic) && s.Channel != ChannelPlay && !s.Lifetime
}

// ReminderStepAt is the reminder due at now for the pass's current end date,
// if any:
//
//   - ReminderDay all through the Dakar day the pass ends, even after the
//     hour it ended: a pass ending at 2 a.m. is told about at 9 a.m.;
//   - ReminderBefore from ReminderLead before the end until that day starts,
//     so a pass given a few days only still gets it, and a day that has both
//     sends only the J0 one.
//
// Whether one was already sent is the store's to say, not the
// subscription's: both steps are logged per end date.
func (s Subscription) ReminderStepAt(now time.Time) (ReminderStep, bool) {
	if !s.Remindable() {
		return "", false
	}
	day := DayStart(s.ExpiresAt)
	switch {
	case !now.Before(day) && now.Before(day.Add(24*time.Hour)):
		return ReminderDay, true
	case now.Before(day) && s.ExpiresAt.Sub(now) <= ReminderLead:
		return ReminderBefore, true
	}
	return "", false
}

// TrialEnd reports whether the pass's end is the 7-day trial's, started at
// started on this box: the trial is the only pass on the box and its end is
// within its days. The hour of slack covers the trial's start and its pass
// being written a moment apart (the back-office counts it the same way).
func (s Subscription) TrialEnd(started time.Time) bool {
	return !started.IsZero() && !s.ExpiresAt.After(started.Add(TrialPass.Duration()+time.Hour))
}

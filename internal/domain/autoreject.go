package domain

import "time"

// Auto-reject (RFC-006, Max): once the household turns it on, a device that
// joins the box and is neither approved nor renamed within AutoRejectDelay is
// refused at the Wi-Fi access control. The devices present when it is turned
// on count as accepted, and the phone running the app is never refused.

// AutoRejectDelay is how long a new device has to be accepted. It is fixed.
const AutoRejectDelay = 15 * time.Minute

// AutoRejectState is where a device stands under the rule.
type AutoRejectState string

const (
	// AutoRejectPending is a device seen since the rule was on, not accepted
	// yet: refused once its delay is over.
	AutoRejectPending AutoRejectState = "pending"
	// AutoRejectAccepted is a device the rule leaves alone for good.
	AutoRejectAccepted AutoRejectState = "accepted"
	// AutoRejectRefused is a device the rule refused. It is never refused
	// twice; restoring its access accepts it.
	AutoRejectRefused AutoRejectState = "refused"
)

// AutoRejectSettings is the household's setting for one box.
type AutoRejectSettings struct {
	Enabled bool
	// Since is when it was last turned on (zero when off).
	Since time.Time
}

// AutoRejectDevice is one device as the rule tracks it.
type AutoRejectDevice struct {
	MAC MAC
	// FirstSeen is when the rule first saw it, the start of its delay.
	FirstSeen time.Time
	State     AutoRejectState
	// DecidedAt is when it was accepted or refused (zero while pending).
	DecidedAt time.Time
}

// Deadline is when a pending device is refused.
func (d AutoRejectDevice) Deadline() time.Time { return d.FirstSeen.Add(AutoRejectDelay) }

// Due reports whether a pending device has run out of time at now.
func (d AutoRejectDevice) Due(now time.Time) bool {
	return d.State == AutoRejectPending && !now.Before(d.Deadline())
}

// AutoRejectRelayWindow is how fresh the relay agent's last hub heartbeat
// must be for it to be the one enforcing the rule (RFC-006, "Who enforces
// it"). Past it, the phone's watch service takes over. One enforcer at a time
// keeps two admin sessions from knocking each other off the box.
const AutoRejectRelayWindow = 2 * time.Minute

// RelayEnforces reports whether a relay whose last hub heartbeat was at last
// is the enforcer at now. The zero time is "no relay". A heartbeat stamped in
// the future (clocks disagree) counts only within the same window, so a
// wrong clock cannot keep the phone standing down for good.
func RelayEnforces(last, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	age := now.Sub(last)
	if age < 0 {
		age = -age
	}
	return age < AutoRejectRelayWindow
}

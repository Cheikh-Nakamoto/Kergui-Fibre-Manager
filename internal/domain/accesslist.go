package domain

import (
	"errors"
	"fmt"
	"time"
)

// The box's deny lists fill up (RFC-007 § Context): every stranger, every new
// private address of a phone and every automatic refusal takes a slot until
// the box refuses to add more. This file holds what the purge of stale rules
// and the « liste pleine » answer share (RFC-007 D6).

// ErrAccessListFull: the box refused to add a rule because its list is full.
// The concrete error is an *AccessListFullError, which wraps it.
var ErrAccessListFull = errors.New("router: access list full")

// The two lists a box can run out of.
const (
	// AccessListFirewall is the firewall MAC filter (« Couper internet »).
	AccessListFirewall = "firewall"
	// AccessListWiFi is the Wi-Fi access control (« Le déconnecter aussi du
	// Wi-Fi »).
	AccessListWiFi = "wifi"
)

// AccessListFullError says which list is full and how full. Limit and Used
// count devices: one firewall rule per device; on the Wi-Fi list a device
// takes one rule per access point, and is counted once. Limit is 0 when the
// box never said it (RFC-007 W1 measures it); a box that refuses an add is
// full at the count it holds, so the adapter reports Limit = Used then.
type AccessListFullError struct {
	Kind  string // AccessListFirewall or AccessListWiFi
	Limit int
	Used  int
}

func (e *AccessListFullError) Error() string {
	if e.Limit <= 0 {
		return fmt.Sprintf("%v (%s, %d used, limit unknown)", ErrAccessListFull, e.Kind, e.Used)
	}
	return fmt.Sprintf("%v (%s, %d/%d)", ErrAccessListFull, e.Kind, e.Used, e.Limit)
}

// Unwrap makes errors.Is(err, ErrAccessListFull) hold.
func (e *AccessListFullError) Unwrap() error { return ErrAccessListFull }

// StaleRuleAge is how long a device must have been absent before kergui
// removes the rule it wrote for it (RFC-007 D6).
const StaleRuleAge = 30 * 24 * time.Hour

// AbsentLongEnough reports whether a device last seen at lastSeen has been
// away StaleRuleAge or more at now: 30 days to the second counts. A zero
// lastSeen is a device the inventory never dated, and is never "absent":
// only a device kergui saw, then stopped seeing, loses its rule.
func AbsentLongEnough(lastSeen, now time.Time) bool {
	if lastSeen.IsZero() {
		return false
	}
	return now.Sub(lastSeen) >= StaleRuleAge
}

package domain

import "time"

// Who enforces auto-reject (RFC-006, #36). The household's relay agent does,
// 24/7, while its last heartbeat to the hub is under Timings.Enforcer old;
// otherwise the phones' watch services do. One at a time, because the box
// allows one admin session and two enforcers would knock each other off it.
//
// The relay that counts is the lease holder — the only one that touches the
// box — and only if it said, on that heartbeat, that it enforces the rule
// (the owner's policy turns it on and a valid Max token covers the box). A
// relay without a pass leaves the rule to the phones.

// Enforcer is the hub's answer to a phone asking whether to stand down.
type Enforcer struct {
	// Relay is true when the relay enforces the rule: the phone stands down.
	Relay bool
	// AgentID, LastHeartbeat and Age name that relay and how fresh it is.
	AgentID       string
	LastHeartbeat time.Time
	Age           time.Duration
}

// DecideEnforcer decides from the lease holder's presence (found false: no
// holder).
func DecideEnforcer(holder Presence, found bool, now time.Time, t Timings) Enforcer {
	t = t.withDefaults()
	if !found || holder.AgentID == "" || !holder.Eligible || !holder.AutoReject {
		return Enforcer{}
	}
	age := now.Sub(holder.SeenAt)
	if age >= t.Enforcer {
		return Enforcer{}
	}
	return Enforcer{Relay: true, AgentID: holder.AgentID, LastHeartbeat: holder.SeenAt, Age: age}
}

// Package domain holds the relay module's rules. The part that decides who
// may talk to the box — eligibility, priority, the lease — is pure, so it is
// tested without Postgres and reads like the design rules of RFC-005.
package domain

import (
	"strings"
	"time"

	agents "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
)

// Timings from RFC-005, design rule 1. The lease lasts three heartbeats, so
// a relay has to miss two in a row before it loses the box.
const (
	LeaseTTL          = 60 * time.Second
	HeartbeatInterval = 20 * time.Second
	// PresenceWindow is how long a heartbeat counts as "this relay is
	// here". It allows one late beat before a standby stops being a
	// candidate.
	PresenceWindow = 45 * time.Second
	// LongPollWait is how long a relay holds the command request open.
	LongPollWait = 25 * time.Second
)

// State is what a heartbeat answers.
type State string

const (
	// StateActive means this relay holds the lease and may reach the box.
	StateActive State = "active"
	// StateStandby means another relay holds it, or this one is not
	// eligible. A standby relay never logs in to the box.
	StateStandby State = "standby"
)

// Report is what a relay tells the hub about itself on each heartbeat.
type Report struct {
	SSID string
	// BSSID is the access point it is associated with. Android needs the
	// location permission to read either; when it is refused the relay
	// sends neither and says so.
	BSSID     string
	RouterPin string
	LANIP     string
	Version   string
	// Released is set by a relay shutting down cleanly, so the next one
	// takes over in a heartbeat instead of waiting out the lease.
	Released bool
}

// Presence is one relay's last heartbeat, as the hub stored it.
type Presence struct {
	AgentID  string
	Kind     agents.Kind
	IsOwner  bool // the agent belongs to the household's owner
	Eligible bool
	Reason   string
	SeenAt   time.Time
}

// Fresh reports whether this presence still counts as a candidate.
func (p Presence) Fresh(now time.Time) bool {
	return now.Sub(p.SeenAt) <= PresenceWindow
}

// Lease is the right to hold the box's single admin session.
type Lease struct {
	TenantID  string
	AgentID   string
	Epoch     int64
	ExpiresAt time.Time
}

// Held reports whether the lease is still running at now.
func (l Lease) Held(now time.Time) bool {
	return l.AgentID != "" && now.Before(l.ExpiresAt)
}

// Eligibility answers RFC-005's design rule 2: is this relay on the right
// network, in front of the right box?
//
// The box identity is the stronger of the two checks and is required. The
// Wi-Fi signature is checked only when the relay reports one: a PC on
// Ethernet has none, and on Android the location permission may be refused.
// Saying so in the reason is what lets the app explain the standby.
func Eligibility(n agents.Network, r Report) (bool, string) {
	if n.RouterPin == "" {
		return false, "the household has not registered its router yet"
	}
	if r.RouterPin == "" {
		return false, "the relay did not report the router's certificate"
	}
	if !strings.EqualFold(strings.TrimSpace(r.RouterPin), strings.TrimSpace(n.RouterPin)) {
		return false, "this is not the household's router"
	}
	if r.SSID == "" && r.BSSID == "" {
		if len(n.SSIDs) == 0 {
			return true, "router identity only (no Wi-Fi signature registered)"
		}
		return true, "router identity only (the relay reports no Wi-Fi)"
	}
	if len(n.SSIDs) > 0 && r.SSID != "" && !contains(n.SSIDs, r.SSID) {
		return false, "the relay is on another Wi-Fi network"
	}
	if len(n.BSSIDs) > 0 && r.BSSID != "" && !contains(n.BSSIDs, r.BSSID) {
		return false, "the relay is on an unknown access point"
	}
	return true, ""
}

// Priority ranks candidates for the lease: a dedicated relay or a PC first,
// then the owner's phone, then anyone else's (RFC-005, design rule 1). A
// higher number wins.
func Priority(p Presence) int {
	switch {
	case p.AgentID == "":
		return 0 // no candidate at all
	case p.Kind == agents.KindDedicated, p.Kind == agents.KindPC:
		return 3
	case p.IsOwner:
		return 2
	default:
		return 1
	}
}

// Outcome is the hub's answer to one heartbeat.
type Outcome struct {
	State State
	Epoch int64
	// ExpiresAt is set when the lease is granted or renewed.
	ExpiresAt time.Time
	// RetryAfter is how long a standby should wait before asking again.
	RetryAfter time.Duration
	Reason     string
	// Grant and Release tell the caller what to write: a lease row for
	// this agent, or no lease at all.
	Grant   bool
	Release bool
}

// Decide applies the lease rules to one heartbeat.
//
// me is the relay that is calling; others are the remaining presences of the
// household, as last stored. cur is the lease in force, zero if none.
//
// Hand-over is deliberate rather than immediate: when a better candidate
// shows up, the holder is told to stand down and the lease is released, and
// the better one takes it on its next heartbeat. The box allows one session
// (C2), so a moment with nobody on it is correct, and two relays at once
// never is.
func Decide(cur Lease, me Presence, others []Presence, r Report, now time.Time) Outcome {
	if r.Released && cur.AgentID == me.AgentID {
		return Outcome{State: StateStandby, Release: true, Reason: "relay is shutting down"}
	}
	if !me.Eligible {
		return Outcome{
			State:      StateStandby,
			RetryAfter: HeartbeatInterval,
			Reason:     me.Reason,
			Release:    cur.AgentID == me.AgentID, // it may no longer be on the right network
		}
	}

	switch {
	case !cur.Held(now):
		// Free, or the holder stopped answering: the best candidate present
		// takes it. Ties go to the caller, which keeps a household with two
		// equal relays from passing the lease back and forth.
		if best := bestOther(others, now); Priority(best) > Priority(me) {
			return standbyUntil(cur, now, "a higher-priority relay is available")
		}
		return Outcome{State: StateActive, Grant: true, Epoch: cur.Epoch + 1, ExpiresAt: now.Add(LeaseTTL)}

	case cur.AgentID == me.AgentID:
		if best := bestOther(others, now); Priority(best) > Priority(me) {
			return Outcome{State: StateStandby, Release: true, RetryAfter: HeartbeatInterval,
				Reason: "handing over to a higher-priority relay"}
		}
		return Outcome{State: StateActive, Epoch: cur.Epoch, ExpiresAt: now.Add(LeaseTTL)}

	default:
		return standbyUntil(cur, now, "another relay holds the lease")
	}
}

// standbyUntil asks a standby to come back when the current lease could
// next change hands, and never later than a heartbeat: with a 60 s lease and
// a 20 s beat, waiting a full beat after an expiry would add up to 80 s of
// silence before anyone takes over.
func standbyUntil(cur Lease, now time.Time, reason string) Outcome {
	retry := HeartbeatInterval
	if left := cur.ExpiresAt.Sub(now); left > 0 && left < retry {
		retry = left
	}
	return Outcome{State: StateStandby, RetryAfter: retry, Reason: reason}
}

// bestOther returns the highest-priority fresh, eligible presence other
// than the caller's own.
func bestOther(others []Presence, now time.Time) Presence {
	var best Presence
	for _, p := range others {
		if !p.Eligible || !p.Fresh(now) {
			continue
		}
		if Priority(p) > Priority(best) {
			best = p
		}
	}
	return best
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

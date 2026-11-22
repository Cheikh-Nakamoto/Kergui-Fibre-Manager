package domain

import "time"

// What the app tells the household about its relay (issue #46): is there one
// on the box right now, one that is heard but cannot reach it, or nobody,
// since when. It is the hub's view of the heartbeats, so it is advisory: the
// app believes a device list or a result only once a relay's signature over
// it checks, whatever this says.

// RelayState is the household's relay situation.
type RelayState string

const (
	// RelayActive: a relay holds the lease and was heard within the
	// presence window. Commands run.
	RelayActive RelayState = "active"
	// RelayStandby: a relay is heard, but none holds the box (wrong Wi-Fi,
	// another box, standing down, between two holders). Commands wait.
	RelayStandby RelayState = "standby"
	// RelayOffline: no relay has been heard within the presence window.
	// LastSeen says since when.
	RelayOffline RelayState = "offline"
	// RelayNone: no relay has ever sent a heartbeat for the household.
	RelayNone RelayState = "none"
)

// RelayStatus is the answer.
type RelayStatus struct {
	State   RelayState
	AgentID string
	// LastSeen is the heartbeat the state rests on: the holder's when
	// active, the freshest one otherwise; zero for RelayNone.
	LastSeen time.Time
	Age      time.Duration
	// Reason is why a heard relay is not eligible, as the heartbeat said.
	Reason string
}

// DecideRelayStatus reads the lease and the household's (non-revoked)
// presences at now.
//
// A holder counts as active only while it is heard: the lease row keeps its
// name until another relay takes it, and a lease that has not run out yet
// says nothing about a phone whose battery just died.
func DecideRelayStatus(lease Lease, presences []Presence, now time.Time, t Timings) RelayStatus {
	t = t.withDefaults()
	var freshest *Presence
	for i := range presences {
		p := &presences[i]
		if freshest == nil || p.SeenAt.After(freshest.SeenAt) {
			freshest = p
		}
		if lease.Held(now) && p.AgentID == lease.AgentID && p.Fresh(now, t.Presence) {
			return RelayStatus{State: RelayActive, AgentID: p.AgentID, LastSeen: p.SeenAt, Age: now.Sub(p.SeenAt)}
		}
	}
	if freshest == nil {
		return RelayStatus{State: RelayNone}
	}
	out := RelayStatus{AgentID: freshest.AgentID, LastSeen: freshest.SeenAt, Age: now.Sub(freshest.SeenAt)}
	if freshest.Fresh(now, t.Presence) {
		out.State = RelayStandby
		if !freshest.Eligible {
			out.Reason = freshest.Reason
		}
		return out
	}
	out.State = RelayOffline
	return out
}

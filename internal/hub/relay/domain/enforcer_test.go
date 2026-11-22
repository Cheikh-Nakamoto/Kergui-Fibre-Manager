package domain

import (
	"testing"
	"time"
)

func TestDecideEnforcer(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	holder := Presence{AgentID: "a1", Eligible: true, AutoReject: true, SeenAt: now.Add(-30 * time.Second)}
	cases := []struct {
		name  string
		p     Presence
		found bool
		want  bool
	}{
		{"no lease holder", Presence{}, false, false},
		{"holder heard 30 s ago, enforcing", holder, true, true},
		{"holder heard 1 min 59 ago", with(holder, func(p *Presence) { p.SeenAt = now.Add(-119 * time.Second) }), true, true},
		{"holder silent for 2 min", with(holder, func(p *Presence) { p.SeenAt = now.Add(-2 * time.Minute) }), true, false},
		{"holder without a valid Max pass", with(holder, func(p *Presence) { p.AutoReject = false }), true, false},
		{"holder no longer on the right network", with(holder, func(p *Presence) { p.Eligible = false }), true, false},
	}
	for _, c := range cases {
		got := DecideEnforcer(c.p, c.found, now, DefaultTimings())
		if got.Relay != c.want {
			t.Errorf("%s: relay = %v, want %v", c.name, got.Relay, c.want)
		}
		if got.Relay && (got.AgentID != "a1" || !got.LastHeartbeat.Equal(c.p.SeenAt) || got.Age != now.Sub(c.p.SeenAt)) {
			t.Errorf("%s: %+v does not name the relay and its heartbeat", c.name, got)
		}
		if !got.Relay && got.AgentID != "" {
			t.Errorf("%s: the phone enforces, yet a relay is named: %+v", c.name, got)
		}
	}
}

func TestEnforcerWindowFollowsTheTimings(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	p := Presence{AgentID: "a1", Eligible: true, AutoReject: true, SeenAt: now.Add(-3 * time.Second)}
	if DecideEnforcer(p, true, now, Timings{Enforcer: 2 * time.Second}).Relay {
		t.Fatal("a shortened window was ignored")
	}
	if DefaultTimings().Enforcer != 2*time.Minute {
		t.Fatalf("default window = %s, want RFC-006's 2 minutes", DefaultTimings().Enforcer)
	}
}

func with(p Presence, f func(*Presence)) Presence { f(&p); return p }

package domain

import (
	"testing"
	"time"
)

func TestDecideRelayStatus(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	tm := DefaultTimings()
	fresh := Presence{AgentID: "a1", Kind: "pc", Eligible: true, SeenAt: now.Add(-10 * time.Second)}
	lease := Lease{AgentID: "a1", Epoch: 3, ExpiresAt: now.Add(40 * time.Second)}

	cases := []struct {
		name      string
		lease     Lease
		presences []Presence
		want      RelayState
		agent     string
		lastSeen  time.Time
		reason    string
	}{
		{"never paired or never heard", Lease{}, nil, RelayNone, "", time.Time{}, ""},
		{"the holder is on the box", lease, []Presence{fresh}, RelayActive, "a1", fresh.SeenAt, ""},
		{
			// #94: the holder yielded the box to the owner at home. It is
			// heard and keeps the lease: paused, never offline.
			"the holder yielded the box to the owner", lease,
			[]Presence{with(fresh, func(p *Presence) { p.Paused = true })},
			RelayPaused, "a1", fresh.SeenAt, "the owner is using the box at home",
		},
		{
			// A paused relay that went silent is offline like any other.
			"a paused holder went silent", lease,
			[]Presence{with(fresh, func(p *Presence) { p.Paused = true; p.SeenAt = now.Add(-2 * time.Minute) })},
			RelayOffline, "a1", now.Add(-2 * time.Minute), "",
		},
		{
			"a relay is there but not eligible", Lease{},
			[]Presence{with(fresh, func(p *Presence) { p.Eligible = false; p.Reason = "the relay is on another Wi-Fi" })},
			RelayStandby, "a1", fresh.SeenAt, "the relay is on another Wi-Fi",
		},
		{
			"the holder's lease ran out, another relay is still heard", Lease{AgentID: "a1", ExpiresAt: now.Add(-time.Second)},
			[]Presence{with(fresh, func(p *Presence) { p.AgentID = "a2"; p.SeenAt = now.Add(-5 * time.Second) })},
			RelayStandby, "a2", now.Add(-5 * time.Second), "",
		},
		{
			"everyone went silent", Lease{AgentID: "a1", ExpiresAt: now.Add(-time.Minute)},
			[]Presence{
				with(fresh, func(p *Presence) { p.SeenAt = now.Add(-10 * time.Minute) }),
				with(fresh, func(p *Presence) { p.AgentID = "a2"; p.SeenAt = now.Add(-3 * time.Minute) }),
			},
			RelayOffline, "a2", now.Add(-3 * time.Minute), "",
		},
		{
			// The lease row outlives a holder that stopped beating: what
			// counts is that it was heard, not that its lease has not run out.
			"the lease is still running but its holder is silent", Lease{AgentID: "a1", ExpiresAt: now.Add(10 * time.Second)},
			[]Presence{with(fresh, func(p *Presence) { p.SeenAt = now.Add(-2 * time.Minute) })},
			RelayOffline, "a1", now.Add(-2 * time.Minute), "",
		},
	}
	for _, c := range cases {
		got := DecideRelayStatus(c.lease, c.presences, now, tm)
		if got.State != c.want {
			t.Errorf("%s: state = %s, want %s", c.name, got.State, c.want)
			continue
		}
		if got.AgentID != c.agent || !got.LastSeen.Equal(c.lastSeen) || got.Reason != c.reason {
			t.Errorf("%s: %+v, want agent %q seen %s reason %q", c.name, got, c.agent, c.lastSeen, c.reason)
		}
		if !c.lastSeen.IsZero() && got.Age != now.Sub(c.lastSeen) {
			t.Errorf("%s: age = %s", c.name, got.Age)
		}
	}
}

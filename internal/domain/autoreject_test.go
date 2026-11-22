package domain

import (
	"testing"
	"time"
)

func TestRelayEnforces(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"no relay ever heartbeated", time.Time{}, false},
		{"heartbeat a second ago", now.Add(-time.Second), true},
		{"heartbeat just under two minutes ago", now.Add(-AutoRejectRelayWindow + time.Second), true},
		{"heartbeat exactly two minutes ago", now.Add(-AutoRejectRelayWindow), false},
		{"heartbeat three minutes ago", now.Add(-3 * time.Minute), false},
		// A relay clock ahead of the phone's must not keep the phone off
		// for ever: a heartbeat from the future counts as fresh only within
		// the same window.
		{"heartbeat slightly in the future", now.Add(30 * time.Second), true},
		{"heartbeat far in the future", now.Add(time.Hour), false},
	}
	for _, c := range cases {
		if got := RelayEnforces(c.last, now); got != c.want {
			t.Errorf("%s: RelayEnforces = %v, want %v", c.name, got, c.want)
		}
	}
}

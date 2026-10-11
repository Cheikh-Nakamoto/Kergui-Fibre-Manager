package domain

import (
	"testing"
	"time"
)

func TestTrafficDelta(t *testing.T) {
	prev := TrafficCounters{Rx: 1000, Tx: 5000, LinkSecs: 600}
	cases := []struct {
		name string
		cur  TrafficCounters
		want uint64
	}{
		{"steady", TrafficCounters{Rx: 1500, Tx: 7000, LinkSecs: 900}, 2500},
		{"idle", prev, 0},
		{"reconnected, counters smaller", TrafficCounters{Rx: 200, Tx: 300, LinkSecs: 30}, 500},
		{"reconnected, counters already larger", TrafficCounters{Rx: 3000, Tx: 9000, LinkSecs: 120}, 12000},
		{"one counter went back", TrafficCounters{Rx: 900, Tx: 6000, LinkSecs: 900}, 6900},
	}
	for _, c := range cases {
		if got := TrafficDelta(prev, c.cur); got != c.want {
			t.Errorf("%s: TrafficDelta = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestBillingPeriod(t *testing.T) {
	if got := BillingPeriod(time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC)); got != "2026-10" {
		t.Errorf("BillingPeriod = %q", got)
	}
}

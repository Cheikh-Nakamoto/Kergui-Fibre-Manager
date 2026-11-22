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

func TestUsageDeltaOf_SplitsByDirection(t *testing.T) {
	prev := TrafficCounters{Rx: 1000, Tx: 5000, LinkSecs: 600}
	cases := []struct {
		name string
		cur  TrafficCounters
		want UsageDelta
	}{
		// Rx is what the box received from the device: the device sent it.
		{"steady", TrafficCounters{Rx: 1500, Tx: 7000, LinkSecs: 900}, UsageDelta{Sent: 500, Received: 2000}},
		{"idle", prev, UsageDelta{}},
		{"reconnected", TrafficCounters{Rx: 200, Tx: 300, LinkSecs: 30}, UsageDelta{Sent: 200, Received: 300}},
		{"one counter went back", TrafficCounters{Rx: 900, Tx: 6000, LinkSecs: 900}, UsageDelta{Sent: 900, Received: 6000}},
	}
	for _, c := range cases {
		got := UsageDeltaOf(prev, c.cur)
		if got != c.want {
			t.Errorf("%s: UsageDeltaOf = %+v, want %+v", c.name, got, c.want)
		}
		if got.Total() != TrafficDelta(prev, c.cur) {
			t.Errorf("%s: Total %d disagrees with TrafficDelta %d", c.name, got.Total(), TrafficDelta(prev, c.cur))
		}
	}
}

func TestUsageDay(t *testing.T) {
	if got := UsageDay(time.Date(2026, 3, 7, 23, 59, 0, 0, time.UTC)); got != "2026-03-07" {
		t.Errorf("UsageDay = %q", got)
	}
}

func TestPeriodsBack(t *testing.T) {
	// Across a year boundary, from the 31st (no month overflow), oldest first.
	got := PeriodsBack(time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), 6)
	want := []string{"2025-10", "2025-11", "2025-12", "2026-01", "2026-02", "2026-03"}
	if len(got) != len(want) {
		t.Fatalf("PeriodsBack = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PeriodsBack = %v, want %v", got, want)
		}
	}
	if got := PeriodsBack(time.Now(), 0); len(got) != 0 {
		t.Errorf("PeriodsBack(0) = %v, want none", got)
	}
}

func TestUsageRetentionCutoff(t *testing.T) {
	// 13 months kept: the current one and the 12 before it.
	if got := UsageRetentionCutoff(time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)); got != "2025-11-01" {
		t.Errorf("UsageRetentionCutoff = %q, want 2025-11-01", got)
	}
}

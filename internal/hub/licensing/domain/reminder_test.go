package domain

import (
	"testing"
	"time"
)

func TestReminderStepAt(t *testing.T) {
	// The pass ends on 24/11 at 15:00 in Dakar (UTC).
	end := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	paid := Subscription{Tier: TierBasic, ExpiresAt: end}
	for _, tc := range []struct {
		name string
		sub  Subscription
		now  time.Time
		want ReminderStep
		ok   bool
	}{
		{"never subscribed", Subscription{ExpiresAt: end}, end.Add(-day), "", false},
		{"free tier", Subscription{Tier: TierFree, ExpiresAt: end}, end.Add(-day), "", false},
		{"more than 3 days before", paid, end.Add(-3*day - time.Second), "", false},
		{"exactly 3 days before", paid, end.Add(-3 * day), ReminderBefore, true},
		{"the evening before", paid, time.Date(2026, 11, 23, 23, 59, 0, 0, time.UTC), ReminderBefore, true},
		{"the day it ends, at midnight", paid, time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC), ReminderDay, true},
		{"the day it ends, after the end", paid, end.Add(time.Hour), ReminderDay, true},
		{"the day after", paid, time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), "", false},
		{"Play renews by itself", Subscription{Tier: TierMax, ExpiresAt: end, Channel: ChannelPlay}, end.Add(-day), "", false},
		{"lifetime", Subscription{Tier: TierMax, ExpiresAt: end, Lifetime: true}, end.Add(-day), "", false},
	} {
		got, ok := tc.sub.ReminderStepAt(tc.now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: ReminderStepAt = %q %v, want %q %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReminderStepAt_AShortPassGetsJ0Only(t *testing.T) {
	// A pass given at 10:00 for the same day: no J-3 step, only J0.
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	s := Subscription{Tier: TierMax, ExpiresAt: now.Add(6 * time.Hour)}
	if step, ok := s.ReminderStepAt(now); !ok || step != ReminderDay {
		t.Fatalf("got %q %v, want J0", step, ok)
	}
}

func TestReminderStepAt_ARenewedPassIsDueAgainForItsNewEnd(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s := Subscription{Tier: TierMax, ExpiresAt: now.Add(2 * day)}
	if _, ok := s.ReminderStepAt(now); !ok {
		t.Fatal("not due before the renewal")
	}
	renewed, err := s.Credit(mustPass(t, TierMax, Term1Month), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := renewed.ReminderStepAt(now); ok {
		t.Error("due right after the renewal")
	}
	if step, ok := renewed.ReminderStepAt(renewed.ExpiresAt.Add(-2 * day)); !ok || step != ReminderBefore {
		t.Errorf("the new end: %q %v", step, ok)
	}
}

func TestReminderHours(t *testing.T) {
	for h, want := range map[int]bool{0: false, 3: false, 8: false, 9: true, 12: true, 19: true, 20: false, 23: false} {
		if got := ReminderHours(time.Date(2026, 11, 24, h, 30, 0, 0, time.UTC)); got != want {
			t.Errorf("%02dh30 in Dakar: ReminderHours = %v, want %v", h, got, want)
		}
	}
	// Dakar is UTC: 10:00 in Paris in winter is 09:00 in Dakar.
	paris := time.FixedZone("CET", 3600)
	if !ReminderHours(time.Date(2026, 11, 24, 10, 0, 0, 0, paris)) {
		t.Error("09:00 in Dakar is within the hours")
	}
}

func TestDayStart(t *testing.T) {
	got := DayStart(time.Date(2026, 11, 24, 23, 59, 0, 0, time.UTC))
	if want := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("DayStart = %s, want %s", got, want)
	}
}

func TestTrialEnd(t *testing.T) {
	started := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	trial := Subscription{Tier: TierMax, ExpiresAt: started.Add(TrialPass.Duration())}
	if !trial.TrialEnd(started) {
		t.Error("the trial's own end")
	}
	paid := Subscription{Tier: TierMax, ExpiresAt: started.Add(TrialPass.Duration() + 30*day)}
	if paid.TrialEnd(started) {
		t.Error("a pass bought on top of the trial ends later: not the trial's end")
	}
	if trial.TrialEnd(time.Time{}) {
		t.Error("no trial started")
	}
}

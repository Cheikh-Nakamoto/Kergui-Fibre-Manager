package domain

import (
	"errors"
	"testing"
	"time"
)

func TestPlayProductTier(t *testing.T) {
	for id, want := range map[string]Tier{"kergui_basic": TierBasic, "kergui_max": TierMax, " kergui_max ": TierMax} {
		got, err := PlayProductTier(id)
		if err != nil || got != want {
			t.Errorf("%q: %v %v, want %v", id, got, err, want)
		}
	}
	for _, id := range []string{"", "kergui_free", "basic_1m", "KERGUI_BASIC"} {
		if _, err := PlayProductTier(id); !errors.Is(err, ErrUnknownProduct) {
			t.Errorf("%q: want ErrUnknownProduct, got %v", id, err)
		}
	}
	if PlayProduct(TierBasic) != "kergui_basic" || PlayProduct(TierMax) != "kergui_max" || PlayProduct(TierFree) != "" {
		t.Error("PlayProduct is not the inverse of PlayProductTier")
	}
}

func TestApplyPlay_SetsGooglesExpiryInsteadOfAddingDays(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	exp := now.Add(31 * 24 * time.Hour)

	// A running prepaid Basic pass is replaced by what Google says: Play is
	// the authority for a Play-billed box.
	s := Subscription{Account: "a", BoxID: "B", Tier: TierBasic, ExpiresAt: now.Add(90 * 24 * time.Hour)}
	got, err := s.ApplyPlay(TierMax, exp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != TierMax || !got.ExpiresAt.Equal(exp) || got.Channel != ChannelPlay {
		t.Fatalf("got %+v", got)
	}
	// A renewal moves the expiry to the new one, whatever it was.
	renewed, _ := got.ApplyPlay(TierMax, exp.Add(30*24*time.Hour))
	if !renewed.ExpiresAt.Equal(exp.Add(30 * 24 * time.Hour)) {
		t.Fatalf("renewed %+v", renewed)
	}
	if _, err := s.ApplyPlay(TierFree, exp); !errors.Is(err, ErrUnknownProduct) {
		t.Errorf("free: %v", err)
	}
	if _, err := s.ApplyPlay(TierMax, time.Time{}); err == nil {
		t.Error("zero expiry accepted")
	}
}

func TestEndPlay_StopsAPlaySubscriptionNowAndLeavesPrepaidAlone(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	play := Subscription{Tier: TierMax, ExpiresAt: now.Add(10 * 24 * time.Hour), Channel: ChannelPlay}
	if got := play.EndPlay(now); !got.ExpiresAt.Equal(now) || got.Active(now) {
		t.Fatalf("ended %+v", got)
	}
	// Already over: the earlier expiry stays.
	past := Subscription{Tier: TierMax, ExpiresAt: now.Add(-time.Hour), Channel: ChannelPlay}
	if got := past.EndPlay(now); !got.ExpiresAt.Equal(past.ExpiresAt) {
		t.Fatalf("past %+v", got)
	}
	prepaid := Subscription{Tier: TierMax, ExpiresAt: now.Add(10 * 24 * time.Hour)}
	if got := prepaid.EndPlay(now); got != prepaid {
		t.Fatalf("prepaid touched: %+v", got)
	}
}

func TestCredit_PutsTheSubscriptionBackOnThePrepaidChannel(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	s := Subscription{Tier: TierBasic, ExpiresAt: now.Add(-time.Hour), Channel: ChannelPlay}
	p, _ := PassByCode("basic_1m")
	got, err := s.Credit(p, now)
	if err != nil || got.Channel != ChannelPrepaid {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestReminderDue_NeverForPlay(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	s := Subscription{Tier: TierMax, ExpiresAt: now.Add(24 * time.Hour)}
	if !s.ReminderDue(now) {
		t.Fatal("prepaid pass ending tomorrow: reminder due")
	}
	s.Channel = ChannelPlay
	if s.ReminderDue(now) {
		t.Fatal("Play renews by itself: no reminder")
	}
}

func TestParseChannel(t *testing.T) {
	for raw, want := range map[string]Channel{"": ChannelPrepaid, "prepaid": ChannelPrepaid, "play": ChannelPlay} {
		if got, err := ParseChannel(raw); err != nil || got != want {
			t.Errorf("%q: %v %v", raw, got, err)
		}
	}
	if _, err := ParseChannel("apple"); err == nil {
		t.Error("apple accepted")
	}
}

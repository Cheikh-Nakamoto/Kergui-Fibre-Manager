package domain

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func mustPass(t *testing.T, tier Tier, term Term) Pass {
	t.Helper()
	p, err := FindPass(tier, term)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseBoxID(t *testing.T) {
	for in, want := range map[string]BoxID{
		"ZTEG1A2B3C4D":   "ZTEG1A2B3C4D",
		" zteg1a2b3c4d ": "ZTEG1A2B3C4D", // OBJ_SN_INFO_ID.Sn has a lower-case hex part
	} {
		got, err := ParseBoxID(in)
		if err != nil || got != want {
			t.Errorf("ParseBoxID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "ZTEG 1A2B", "ZTEG/1A2B", "192.168.1.1", string(make([]byte, 65))} {
		if _, err := ParseBoxID(bad); !errors.Is(err, ErrInvalidBoxID) {
			t.Errorf("ParseBoxID(%q) = %v, want ErrInvalidBoxID", bad, err)
		}
	}
}

// TestParsePhone: the account is the alerts module's E.164 number, so the
// same person is the same number in both.
func TestParsePhone(t *testing.T) {
	got, err := ParsePhone("+221 77 123 45 67")
	if err != nil || got != Phone("+221771234567") {
		t.Errorf("ParsePhone = %q, %v", got, err)
	}
	if _, err := ParsePhone("pas un numéro"); err == nil {
		t.Error("a bad number was accepted")
	}
}

func TestEffectiveTier(t *testing.T) {
	for _, tc := range []struct {
		name string
		sub  Subscription
		now  time.Time
		want Tier
	}{
		{"never subscribed", Subscription{}, t0, TierFree},
		{"active Basic", Subscription{Tier: TierBasic, ExpiresAt: t0.Add(day)}, t0, TierBasic},
		{"active Max", Subscription{Tier: TierMax, ExpiresAt: t0.Add(time.Second)}, t0, TierMax},
		{"expired exactly now", Subscription{Tier: TierMax, ExpiresAt: t0}, t0, TierFree},
		{"expired", Subscription{Tier: TierBasic, ExpiresAt: t0.Add(-day)}, t0, TierFree},
		{"corrupt tier", Subscription{Tier: Tier("gold"), ExpiresAt: t0.Add(day)}, t0, TierFree},
	} {
		if got := tc.sub.EffectiveTier(tc.now); got != tc.want {
			t.Errorf("%s: EffectiveTier = %s, want %s", tc.name, got, tc.want)
		}
		if got := tc.sub.Active(tc.now); got != (tc.want != TierFree) {
			t.Errorf("%s: Active = %v", tc.name, got)
		}
	}
}

// TestConvertBasicToMax: remaining Basic time becomes Max time at the
// monthly price ratio, 650 → 2 000, rounded down to the whole hour.
func TestConvertBasicToMax(t *testing.T) {
	for _, tc := range []struct {
		in, want time.Duration
	}{
		{0, 0},
		{-day, 0},
		{30 * day, 234 * time.Hour},                   // 720 h × 0.325 = 234 h: a full Basic month is 9.75 Max days
		{20 * day, 156 * time.Hour},                   // 480 h × 0.325 = 156 h
		{10*day + 7*time.Hour, 80 * time.Hour},        // 247 h × 0.325 = 80.275 h
		{3 * time.Hour, 0},                            // 0.975 h rounds down
		{365 * day, 2847 * time.Hour},                 // 8760 h × 0.325 = 2847 h, no overflow
		{4*time.Hour + 59*time.Minute, 1 * time.Hour}, // 1.6 h
	} {
		if got := ConvertBasicToMax(tc.in); got != tc.want {
			t.Errorf("ConvertBasicToMax(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCredit(t *testing.T) {
	basic1m := mustPass(t, TierBasic, Term1Month)
	basic3m := mustPass(t, TierBasic, Term3Months)
	max1m := mustPass(t, TierMax, Term1Month)
	max12m := mustPass(t, TierMax, Term12Months)
	for _, tc := range []struct {
		name     string
		sub      Subscription
		pass     Pass
		wantTier Tier
		wantExp  time.Time
	}{
		{"first pass starts now", Subscription{}, basic1m, TierBasic, t0.Add(30 * day)},
		{"after expiry starts now, not from the old expiry",
			Subscription{Tier: TierMax, ExpiresAt: t0.Add(-10 * day)}, basic1m, TierBasic, t0.Add(30 * day)},
		{"same tier extends from the current expiry",
			Subscription{Tier: TierBasic, ExpiresAt: t0.Add(5 * day)}, basic3m, TierBasic, t0.Add(95 * day)},
		{"Max on Max extends", Subscription{Tier: TierMax, ExpiresAt: t0.Add(2 * day)}, max12m, TierMax, t0.Add(367 * day)},
		{"upgrade converts remaining Basic days, then adds the Max pass",
			Subscription{Tier: TierBasic, ExpiresAt: t0.Add(20 * day)}, max1m, TierMax, t0.Add(156*time.Hour + 30*day)},
		{"upgrade on an expired Basic pass has nothing to convert",
			Subscription{Tier: TierBasic, ExpiresAt: t0.Add(-day)}, max1m, TierMax, t0.Add(30 * day)},
		{"a paid Basic pass landing on an active Max is converted into Max days",
			Subscription{Tier: TierMax, ExpiresAt: t0.Add(4 * day)}, basic1m, TierMax, t0.Add(4*day + 234*time.Hour)},
		{"trial on a new box", Subscription{}, TrialPass, TierMax, t0.Add(7 * day)},
	} {
		got, err := tc.sub.Credit(tc.pass, t0)
		if err != nil {
			t.Errorf("%s: Credit = %v", tc.name, err)
			continue
		}
		if got.Tier != tc.wantTier || !got.ExpiresAt.Equal(tc.wantExp) {
			t.Errorf("%s: Credit = %s until %v, want %s until %v", tc.name, got.Tier, got.ExpiresAt, tc.wantTier, tc.wantExp)
		}
	}
}

func TestCreditKeepsIdentity(t *testing.T) {
	sub := Subscription{Account: "acc-1", BoxID: "ZTEG00000001"}
	got, err := sub.Credit(mustPass(t, TierMax, Term1Month), t0)
	if err != nil || got.Account != sub.Account || got.BoxID != sub.BoxID {
		t.Errorf("Credit = %+v, %v", got, err)
	}
}

func TestCreditRejectsBadPass(t *testing.T) {
	for _, p := range []Pass{
		{},
		{Tier: TierFree, Days: 30},
		{Tier: TierMax, Days: 0},
		{Tier: TierBasic, Days: -1},
		{Tier: Tier("gold"), Days: 30},
	} {
		if _, err := (Subscription{}).Credit(p, t0); !errors.Is(err, ErrUnknownPass) {
			t.Errorf("Credit(%+v) = %v, want ErrUnknownPass", p, err)
		}
	}
}

// TestCanBuy: the purchase page does not sell a Basic pass while Max runs;
// Credit still honours one if it was paid anyway.
func TestCanBuy(t *testing.T) {
	basic := mustPass(t, TierBasic, Term1Month)
	max := mustPass(t, TierMax, Term1Month)
	activeMax := Subscription{Tier: TierMax, ExpiresAt: t0.Add(day)}
	for _, tc := range []struct {
		name string
		sub  Subscription
		pass Pass
		want error
	}{
		{"Basic on nothing", Subscription{}, basic, nil},
		{"Max on Basic", Subscription{Tier: TierBasic, ExpiresAt: t0.Add(day)}, max, nil},
		{"Max on Max", activeMax, max, nil},
		{"Basic on expired Max", Subscription{Tier: TierMax, ExpiresAt: t0}, basic, nil},
		{"Basic on active Max", activeMax, basic, ErrMaxActive},
		{"the trial is not for sale", Subscription{}, TrialPass, ErrUnknownPass},
		{"a made-up pass", Subscription{}, Pass{Code: "max_1m", Tier: TierMax, Days: 3000}, ErrUnknownPass},
	} {
		if err := tc.sub.CanBuy(tc.pass, t0); !errors.Is(err, tc.want) {
			t.Errorf("%s: CanBuy = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestCheckTrial(t *testing.T) {
	for _, tc := range []struct {
		phone, box bool
		want       error
	}{
		{false, false, nil},
		{true, false, ErrTrialPhoneUsed},
		{false, true, ErrTrialBoxUsed},
		{true, true, ErrTrialPhoneUsed},
	} {
		if err := CheckTrial(tc.phone, tc.box); !errors.Is(err, tc.want) {
			t.Errorf("CheckTrial(%v, %v) = %v, want %v", tc.phone, tc.box, err, tc.want)
		}
		sub, err := (Subscription{}).StartTrial(tc.phone, tc.box, t0)
		if !errors.Is(err, tc.want) {
			t.Errorf("StartTrial(%v, %v) = %v, want %v", tc.phone, tc.box, err, tc.want)
		}
		if tc.want == nil && (sub.Tier != TierMax || !sub.ExpiresAt.Equal(t0.Add(7*day))) {
			t.Errorf("StartTrial = %+v", sub)
		}
	}
}

func TestCheckTransfer(t *testing.T) {
	for _, tc := range []struct {
		name string
		past []time.Time
		want error
	}{
		{"never", nil, nil},
		{"one this month", []time.Time{t0.Add(-day)}, nil},
		{"two this month", []time.Time{t0.Add(-day), t0.Add(-20 * day)}, ErrTransferLimit},
		{"two, one just out of the window", []time.Time{t0.Add(-day), t0.Add(-30 * day)}, nil},
		{"two, one just inside the window", []time.Time{t0.Add(-day), t0.Add(-30*day + time.Second)}, ErrTransferLimit},
		{"many, long ago", []time.Time{t0.Add(-40 * day), t0.Add(-50 * day), t0.Add(-60 * day)}, nil},
		// A timestamp after now (clock skew between hub instances) still counts.
		{"one recorded in the future", []time.Time{t0.Add(time.Hour), t0.Add(-day)}, ErrTransferLimit},
	} {
		if err := CheckTransfer(tc.past, t0); !errors.Is(err, tc.want) {
			t.Errorf("%s: CheckTransfer = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestTransferTo(t *testing.T) {
	sub := Subscription{Account: "acc-1", BoxID: "ZTEG00000001", Tier: TierMax, ExpiresAt: t0.Add(10 * day),
		ReminderSentFor: t0.Add(10 * day)}
	got, err := sub.TransferTo("ZTEG00000002", nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.BoxID != "ZTEG00000002" || got.Tier != sub.Tier || !got.ExpiresAt.Equal(sub.ExpiresAt) || got.Account != sub.Account {
		t.Errorf("TransferTo = %+v", got)
	}
	for _, tc := range []struct {
		name string
		sub  Subscription
		to   BoxID
		past []time.Time
		want error
	}{
		{"same box", sub, sub.BoxID, nil, ErrSameBox},
		{"no box", sub, "", nil, ErrInvalidBoxID},
		{"limit reached", sub, "ZTEG00000002", []time.Time{t0.Add(-day), t0.Add(-2 * day)}, ErrTransferLimit},
		{"nothing to transfer", Subscription{BoxID: "ZTEG00000001", Tier: TierMax, ExpiresAt: t0}, "ZTEG00000002", nil, ErrNoActivePass},
	} {
		if _, err := tc.sub.TransferTo(tc.to, tc.past, t0); !errors.Is(err, tc.want) {
			t.Errorf("%s: TransferTo = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestReminderDue(t *testing.T) {
	exp := t0.Add(3 * day)
	for _, tc := range []struct {
		name string
		sub  Subscription
		want bool
	}{
		{"never subscribed", Subscription{}, false},
		{"more than 3 days left", Subscription{Tier: TierBasic, ExpiresAt: t0.Add(3*day + time.Second)}, false},
		{"exactly 3 days left", Subscription{Tier: TierBasic, ExpiresAt: exp}, true},
		{"one hour left", Subscription{Tier: TierMax, ExpiresAt: t0.Add(time.Hour)}, true},
		{"already sent for this expiry", Subscription{Tier: TierMax, ExpiresAt: exp, ReminderSentFor: exp}, false},
		{"sent for an earlier expiry, then renewed", Subscription{Tier: TierMax, ExpiresAt: exp, ReminderSentFor: t0.Add(-27 * day)}, true},
		{"already expired", Subscription{Tier: TierMax, ExpiresAt: t0}, false},
	} {
		if got := tc.sub.ReminderDue(t0); got != tc.want {
			t.Errorf("%s: ReminderDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReminderSent(t *testing.T) {
	sub := Subscription{Tier: TierMax, ExpiresAt: t0.Add(day)}.ReminderSent()
	if sub.ReminderDue(t0) {
		t.Error("a reminder is due again right after being sent")
	}
	renewed, err := sub.Credit(mustPass(t, TierMax, Term1Month), t0)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.ReminderDue(t0.Add(28 * day)) {
		t.Error("a renewed pass gets no reminder before its new expiry")
	}
}

func TestExtend(t *testing.T) {
	running := Subscription{Account: "a", BoxID: "B1", Tier: TierMax, ExpiresAt: t0.Add(5 * day)}
	got, err := running.Extend(3, t0)
	if err != nil || got.Tier != TierMax || !got.ExpiresAt.Equal(t0.Add(8*day)) {
		t.Fatalf("running pass: %+v %v", got, err)
	}
	// A pass that ran out restarts from now, on the tier it had.
	ended := Subscription{Account: "a", BoxID: "B1", Tier: TierBasic, ExpiresAt: t0.Add(-10 * day)}
	if got, err = ended.Extend(7, t0); err != nil || got.Tier != TierBasic || !got.ExpiresAt.Equal(t0.Add(7*day)) {
		t.Fatalf("ended pass: %+v %v", got, err)
	}
	if _, err := (Subscription{Tier: TierFree, ExpiresAt: t0}).Extend(3, t0); !errors.Is(err, ErrNoActivePass) {
		t.Fatalf("free box: %v", err)
	}
	for _, d := range []int{0, -1, MaxExtendDays + 1} {
		if _, err := running.Extend(d, t0); !errors.Is(err, ErrExtendDays) {
			t.Fatalf("Extend(%d) = %v", d, err)
		}
	}
}

package domain

import (
	"errors"
	"testing"
	"time"
)

// The closed Wi-Fi (RFC-007 D1, D3, D4, D7): places, the target the household
// APs converge to, the device's trust state and the stored intent.

const f6600pWiFiLimit = 64 // RFC-007 W1: one table shared by every AP

func lockBox(rules map[string]int, limit int) WiFiLockBox {
	b := WiFiLockBox{Rules: map[string][]MAC{}, Limit: limit}
	n := 0
	for ap, count := range rules {
		b.APs = append(b.APs, WiFiLockAP{ID: ap, Mode: WiFiModeDisabled})
		for i := 0; i < count; i++ {
			n++
			b.Rules[ap] = append(b.Rules[ap], fakeMAC(n))
		}
	}
	b.Used = n
	return b
}

// fakeMAC is a distinct fake private address per n.
func fakeMAC(n int) MAC {
	return MustMAC("02:00:00:00:" + hex2(n>>8) + ":" + hex2(n))
}

func hex2(n int) string { return string([]byte{hexdigits[(n>>4)&0xf], hexdigits[n&0xf]}) }

func TestWiFiPlaces_MeasuredOnTheF6600P(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		open   int // rules on the APs that stay open
		closed int
		want   int
		known  bool
	}{
		{"2 closed APs", f6600pWiFiLimit, 0, 2, 32, true},
		{"3 closed APs", f6600pWiFiLimit, 0, 3, 21, true},
		{"rules left on an open AP", f6600pWiFiLimit, 4, 2, 30, true},
		{"rounded down", f6600pWiFiLimit, 1, 3, 21, true},
		{"open APs take the whole table", f6600pWiFiLimit, 70, 2, 0, true},
		{"limit unknown", 0, 0, 2, 0, false},
		{"no closed AP", f6600pWiFiLimit, 0, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, known := WiFiPlaces(c.limit, c.open, c.closed)
			if got != c.want || known != c.known {
				t.Errorf("WiFiPlaces(%d, %d, %d) = (%d, %t), want (%d, %t)", c.limit, c.open, c.closed, got, known, c.want, c.known)
			}
		})
	}
}

func TestWiFiLockBox_PlacesCountsOnlyTheAPsThatStayOpen(t *testing.T) {
	// AP1/AP5/AP7 hold the household's 3×21 deny rules, AP2 a rule of the
	// household's own; closing AP1 and AP5 frees their rules, not AP2's or
	// AP7's.
	b := lockBox(map[string]int{"AP1": 21, "AP5": 21, "AP7": 21, "AP2": 1}, f6600pWiFiLimit)
	if got, known := b.Places([]string{"AP1", "AP5"}); got != 21 || !known {
		t.Errorf("Places(AP1, AP5) = (%d, %t), want (21, true): (64 − 22) / 2", got, known)
	}
	if got, known := b.Places([]string{"AP1", "AP5", "AP7"}); got != 21 || !known {
		t.Errorf("Places(AP1, AP5, AP7) = (%d, %t), want (21, true): (64 − 1) / 3", got, known)
	}
	if got, known := lockBox(nil, f6600pWiFiLimit).Places([]string{"AP1", "AP5"}); got != 32 || !known {
		t.Errorf("an empty table, 2 closed = (%d, %t), want (32, true)", got, known)
	}
	if _, known := lockBox(map[string]int{"AP1": 3}, 0).Places([]string{"AP1"}); known {
		t.Error("a box that never said its limit has known places")
	}
}

func TestWiFiLockBox_CheckFits(t *testing.T) {
	b := lockBox(nil, f6600pWiFiLimit)
	macs := func(n int) []MAC {
		out := make([]MAC, n)
		for i := range out {
			out[i] = fakeMAC(i + 1)
		}
		return out
	}
	aps := []string{"AP1", "AP5", "AP7"}
	if err := b.CheckFits(WiFiLockTarget{APs: aps, Mode: WiFiModeAllow, MACs: macs(21)}); err != nil {
		t.Errorf("21 devices on 3 APs: %v, want them to fit", err)
	}
	err := b.CheckFits(WiFiLockTarget{APs: aps, Mode: WiFiModeAllow, MACs: macs(22)})
	var full *AccessListFullError
	if !errors.As(err, &full) || !errors.Is(err, ErrAccessListFull) {
		t.Fatalf("22 devices on 3 APs: %v, want an AccessListFullError", err)
	}
	if full.Kind != AccessListWiFi || full.Limit != 21 || full.Used != 22 {
		t.Errorf("full = %+v, want wifi 22/21", *full)
	}
	if err := lockBox(nil, 0).CheckFits(WiFiLockTarget{APs: aps, Mode: WiFiModeAllow, MACs: macs(40)}); err != nil {
		t.Errorf("limit unknown: %v, want no refusal before the box answers", err)
	}
}

func TestWiFiLockTarget_Validate(t *testing.T) {
	phone := MustMAC("aa:bb:cc:00:00:01")
	ok := WiFiLockTarget{APs: []string{"AP1", "AP5"}, Mode: WiFiModeAllow, MACs: []MAC{phone}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a valid target: %v", err)
	}
	for _, c := range []struct {
		name string
		t    WiFiLockTarget
		want error
	}{
		// W2: Allow with an empty list does nothing on the F6600P, and
		// emptying an AP already in Allow refuses everyone.
		{"Allow with no device", WiFiLockTarget{APs: []string{"AP1"}, Mode: WiFiModeAllow}, ErrEmptyAllowList},
		{"Allow with only an unknown MAC", WiFiLockTarget{APs: []string{"AP1"}, Mode: WiFiModeAllow, MACs: []MAC{{}}}, ErrInvalidMAC},
		{"no AP", WiFiLockTarget{Mode: WiFiModeAllow, MACs: []MAC{phone}}, ErrInvalidInput},
		{"an empty AP", WiFiLockTarget{APs: []string{" "}, Mode: WiFiModeAllow, MACs: []MAC{phone}}, ErrInvalidInput},
		{"the same AP twice", WiFiLockTarget{APs: []string{"AP1", "AP1"}, Mode: WiFiModeAllow, MACs: []MAC{phone}}, ErrInvalidInput},
		{"unknown mode", WiFiLockTarget{APs: []string{"AP1"}, Mode: "Whitelist", MACs: []MAC{phone}}, ErrInvalidAccessMode},
		{"the same device twice", WiFiLockTarget{APs: []string{"AP1"}, Mode: WiFiModeAllow, MACs: []MAC{phone, phone}}, ErrInvalidInput},
	} {
		if err := c.t.Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	// Disabled and Ban may hold no rule: nothing is filtered then.
	for _, mode := range []string{WiFiModeDisabled, WiFiModeBan} {
		if err := (WiFiLockTarget{APs: []string{"AP1"}, Mode: mode}).Validate(); err != nil {
			t.Errorf("%s with no device: %v, want valid", mode, err)
		}
	}
}

func TestCheckWiFiModeSwitch_NeverAllowOnAnEmptyAP(t *testing.T) {
	phone := MustMAC("aa:bb:cc:00:00:01")
	b := WiFiLockBox{
		APs:   []WiFiLockAP{{ID: "AP1", Mode: WiFiModeDisabled}, {ID: "AP5", Mode: WiFiModeDisabled}},
		Rules: map[string][]MAC{"AP1": {phone}},
	}
	if err := b.CheckModeSwitch([]string{"AP1"}, WiFiModeAllow); err != nil {
		t.Errorf("AP1 holds this phone: %v", err)
	}
	if err := b.CheckModeSwitch([]string{"AP1", "AP5"}, WiFiModeAllow); !errors.Is(err, ErrEmptyAllowList) {
		t.Errorf("AP5 holds no rule: %v, want ErrEmptyAllowList", err)
	}
	if err := b.CheckModeSwitch([]string{"AP5"}, WiFiModeDisabled); err != nil {
		t.Errorf("Disabled on an empty AP: %v", err)
	}
	if err := b.CheckModeSwitch([]string{"AP9"}, WiFiModeDisabled); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("an AP the box does not have: %v, want ErrInvalidInput", err)
	}
	if err := b.CheckModeSwitch(nil, WiFiModeDisabled); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("no AP: %v, want ErrInvalidInput", err)
	}
	if err := b.CheckModeSwitch([]string{"AP1"}, "Open"); !errors.Is(err, ErrInvalidAccessMode) {
		t.Errorf("unknown mode: %v, want ErrInvalidAccessMode", err)
	}
}

func TestHouseholdAllowList_AlwaysHoldsThisPhoneRelaysAndOwners(t *testing.T) {
	phone := MustMAC("aa:bb:cc:00:00:01")
	relay := MustMAC("aa:bb:cc:00:00:02")
	owner := MustMAC("aa:bb:cc:00:00:03")
	tv := MustMAC("aa:bb:cc:00:00:04")
	blocked := MustMAC("aa:bb:cc:00:00:05")
	h := WiFiHousehold{
		Approved: []MAC{tv, blocked, tv, {}},
		Blocked:  []MAC{blocked, owner},
		Self:     phone,
		Relays:   []MAC{relay},
		Owners:   []MAC{owner},
	}
	tg, err := HouseholdAllowList([]string{"AP1", "AP5"}, WiFiModeAllow, h)
	if err != nil {
		t.Fatal(err)
	}
	want := []MAC{phone, relay, owner, tv}
	if len(tg.MACs) != len(want) {
		t.Fatalf("MACs = %v, want %v", tg.MACs, want)
	}
	for i := range want {
		if !tg.MACs[i].Equal(want[i]) {
			t.Fatalf("MACs = %v, want %v (sorted, no duplicate, the blocked one out, the owner kept)", tg.MACs, want)
		}
	}
	if tg.Mode != WiFiModeAllow || len(tg.APs) != 2 {
		t.Errorf("target = %+v", tg)
	}

	// Through the relay this phone may be unknown: the relay is enough.
	if _, err := HouseholdAllowList([]string{"AP1"}, WiFiModeAllow, WiFiHousehold{Relays: []MAC{relay}}); err != nil {
		t.Errorf("relay only: %v", err)
	}
	// Nobody who must stay: refused, the list would be empty or lock out.
	if _, err := HouseholdAllowList([]string{"AP1"}, WiFiModeAllow, WiFiHousehold{Approved: []MAC{tv}}); !errors.Is(err, ErrSelfLockout) {
		t.Errorf("no phone and no relay: %v, want ErrSelfLockout", err)
	}
	// Ban is not an allow list.
	if _, err := HouseholdAllowList([]string{"AP1"}, WiFiModeBan, h); !errors.Is(err, ErrInvalidAccessMode) {
		t.Errorf("Ban: %v, want ErrInvalidAccessMode", err)
	}
	// Disabled keeps the allow list (open, light).
	if tg, err := HouseholdAllowList([]string{"AP1"}, WiFiModeDisabled, h); err != nil || len(tg.MACs) != 4 {
		t.Errorf("Disabled = %+v, %v", tg, err)
	}
}

func TestDeviceTrust_AuthorisedAndBlockedExcludeEachOther(t *testing.T) {
	var a DeviceAccess
	if a.Trust() != TrustToCheck {
		t.Fatalf("default = %s, want à vérifier", a.Trust())
	}
	a = a.Authorise()
	if a.Trust() != TrustAuthorised || a.Blocked || a.WiFiCut {
		t.Fatalf("authorised = %+v", a)
	}
	a = a.Block(true)
	if a.Trust() != TrustBlocked || a.Approved || !a.WiFiCut {
		t.Fatalf("blocking an authorised device must withdraw the approval: %+v", a)
	}
	a = a.Authorise()
	if a.Trust() != TrustAuthorised || a.Blocked || a.WiFiCut {
		t.Fatalf("authorising a blocked device must unblock it: %+v", a)
	}
	a = a.Block(false).Unblock()
	if a.Trust() != TrustToCheck || a.Approved || a.Blocked || a.WiFiCut {
		t.Fatalf("unblocked = %+v, want à vérifier", a)
	}
	a = a.Authorise().ToCheck()
	if a.Trust() != TrustToCheck || a.Approved {
		t.Fatalf("to check = %+v", a)
	}
	// Data from two stores that disagree: the block wins, the box enforces it.
	if (DeviceAccess{Approved: true, Blocked: true}).Trust() != TrustBlocked {
		t.Error("approved and blocked at once does not read as blocked")
	}
	// The Wi-Fi refusal only goes with a block.
	if (DeviceAccess{WiFiCut: true}).Trust() != TrustToCheck {
		t.Error("a Wi-Fi refusal alone reads as blocked")
	}
}

func TestWiFiLockIntent_SelfApprovalOnlyWhileOpen(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if !(WiFiLockIntent{}).SelfApproval(now) {
		t.Error("open Wi-Fi: self-approval refused")
	}
	if (WiFiLockIntent{Closed: true, Since: now.Add(-time.Hour)}).SelfApproval(now) {
		t.Error("closed Wi-Fi: self-approval allowed (G6)")
	}
	window := WiFiLockIntent{Closed: true, WindowUntil: now.Add(3 * time.Minute)}
	if window.SelfApproval(now) || !window.WindowOpen(now) {
		t.Error("add window: self-approval allowed, or the window is not open")
	}
	if window.WindowOpen(now.Add(3 * time.Minute)) {
		t.Error("the window is still open at its deadline")
	}
	// A window nobody closed leaves the Wi-Fi closed in intent: still no
	// self-approval.
	if window.SelfApproval(now.Add(time.Hour)) {
		t.Error("past the window: self-approval allowed while the intent is closed")
	}
}

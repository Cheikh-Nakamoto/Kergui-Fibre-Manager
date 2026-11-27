package ztef6600p

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// The closed Wi-Fi on the F6600P (RFC-007 D4, D7, #105), against the mock
// box. The mock records every write that reads a list the wrong way
// (WiFiHazards): Ban straight to Allow with deny rules on the AP (the trap),
// Allow with an empty list (W2), the last rule of an AP in Allow removed (W2
// bis). Every test here ends with none.

const (
	ap1, ap2, ap5, ap7 = "DEV.WIFI.AP1", "DEV.WIFI.AP2", "DEV.WIFI.AP5", "DEV.WIFI.AP7"

	phone    = "02:aa:bb:00:01:01" // this phone
	relay    = "02:aa:bb:00:01:02" // the household's relay
	laptop   = "02:aa:bb:00:01:03" // an approved device
	denied1  = "02:aa:bb:00:02:01" // Wi-Fi refusals kergui wrote
	denied2  = "02:aa:bb:00:02:02"
	theirs   = "02:aa:bb:00:02:03" // a refusal the household typed in the box's page
	stranger = "02:aa:bb:00:03:01" // on the Wi-Fi, never approved
)

var everyAP = []string{ap1, ap2, ap5, ap7}

// wifiWire counts the Wi-Fi access-control writes the gateway sends to the
// mock, and lets a test act right after the n-th one.
type wifiWire struct {
	mu     sync.Mutex
	writes []string // "<tag> <IF_ACTION>"
	after  func(n int)
}

func (w *wifiWire) policyWrites() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, s := range w.writes {
		if strings.HasPrefix(s, wlanPolicyTag) {
			n++
		}
	}
	return n
}

// wiredBox serves box behind a wifiWire.
func wiredBox(t *testing.T, box *mockrouter.F6600P) (string, *wifiWire) {
	t.Helper()
	wire := &wifiWire{}
	h := box.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("_tag")
		isWiFi := r.Method == http.MethodPost && (tag == wlanRuleTag || tag == wlanPolicyTag)
		if isWiFi {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			vals, _ := url.ParseQuery(string(body))
			wire.mu.Lock()
			wire.writes = append(wire.writes, tag+" "+vals.Get("IF_ACTION"))
			n := len(wire.writes)
			after := wire.after
			wire.mu.Unlock()
			h.ServeHTTP(w, r)
			if after != nil {
				after(n)
			}
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, wire
}

func gatewayAt(t *testing.T, base string, opts port.RouterOptions) *Gateway {
	t.Helper()
	gw, err := New(base, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Login(context.Background(), domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatal(err)
	}
	return gw
}

// banBox is a box "open, with Wi-Fi refusals" (RFC-007 D4): AP1 and AP5 in
// Ban with kergui's deny rules and one the household typed, AP2 off. This
// phone is on AP1, a stranger on AP5.
func banBox() *mockrouter.F6600P {
	box := mockrouter.NewF6600P("admin", "admin")
	for _, ap := range []string{ap1, ap5} {
		box.SetWiFiPolicy(ap, domain.WiFiModeBan)
		for _, m := range []string{denied1, denied2} {
			box.AddWiFiRule(m, ap, wlanRuleName(mustMACv(m), ap))
		}
	}
	box.AddWiFiRule(theirs, ap1, "Tablette voisin")
	box.Join(mockrouter.Station{MAC: phone, AP: ap1})
	box.Join(mockrouter.Station{MAC: stranger, AP: ap5})
	return box
}

// allowBox is a box already closed on AP1 and AP5 with macs.
func allowBox(macs ...string) *mockrouter.F6600P {
	box := mockrouter.NewF6600P("admin", "admin")
	for _, ap := range []string{ap1, ap5} {
		for _, m := range macs {
			box.AddWiFiRule(m, ap, wlanRuleName(mustMACv(m), ap))
		}
		box.SetWiFiPolicy(ap, domain.WiFiModeAllow)
	}
	box.Join(mockrouter.Station{MAC: phone, AP: ap1})
	return box
}

// lightBox is a box "open, light": AP1 and AP5 in Disabled, the allow list
// kept on them.
func lightBox(macs ...string) *mockrouter.F6600P {
	box := mockrouter.NewF6600P("admin", "admin")
	for _, ap := range []string{ap1, ap5} {
		for _, m := range macs {
			box.AddWiFiRule(m, ap, wlanRuleName(mustMACv(m), ap))
		}
	}
	box.Join(mockrouter.Station{MAC: phone, AP: ap1})
	return box
}

func closeTo(aps []string, macs ...string) domain.WiFiLockTarget {
	t := domain.WiFiLockTarget{APs: aps, Mode: domain.WiFiModeAllow}
	for _, m := range macs {
		t.MACs = append(t.MACs, mustMACv(m))
	}
	return t
}

func mustMACv(s string) domain.MAC {
	m, err := domain.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

// boxState is what a rollback must give back: every mode and every rule
// (device, AP, name), whatever its id.
type boxState struct {
	modes map[string]string
	rules []string
}

func stateOf(box *mockrouter.F6600P) boxState {
	s := boxState{modes: map[string]string{}}
	for _, ap := range everyAP {
		s.modes[ap] = box.WiFiPolicy(ap)
	}
	for _, r := range box.WiFiRules() {
		s.rules = append(s.rules, strings.ToLower(r.MAC)+" "+r.AP+" "+r.Name)
	}
	slices.Sort(s.rules)
	return s
}

func (s boxState) String() string { return fmt.Sprintf("modes %v, rules %v", s.modes, s.rules) }

func sameState(a, b boxState) bool {
	return fmt.Sprint(a.modes) == fmt.Sprint(b.modes) && slices.Equal(a.rules, b.rules)
}

// listed returns the devices listed on ap, sorted.
func listed(box *mockrouter.F6600P, ap string) []string {
	var out []string
	for _, r := range box.WiFiRules() {
		if r.AP == ap {
			out = append(out, strings.ToLower(r.MAC))
		}
	}
	slices.Sort(out)
	return out
}

func noHazards(t *testing.T, box *mockrouter.F6600P) {
	t.Helper()
	if h := box.WiFiHazards(); len(h) != 0 {
		t.Errorf("the box read a list the wrong way: %v", h)
	}
}

func associated(box *mockrouter.F6600P, mac string) bool {
	for _, st := range box.Stations() {
		if strings.EqualFold(st.MAC, mac) {
			return st.Associated
		}
	}
	return false
}

// TestApplyWiFiLock_ClosesWithoutTheTrap: from deny rules in Ban, the APs go
// to Disabled first, lose their refusals (the household's too: an AP cannot
// hold both lists), get the allow list, and only then switch to Allow. This
// phone stays on, the stranger is dropped, the refused devices stay out.
func TestApplyWiFiLock_ClosesWithoutTheTrap(t *testing.T) {
	box := banBox()
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	if err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5}, phone, relay)); err != nil {
		t.Fatalf("ApplyWiFiLock: %v", err)
	}
	noHazards(t, box)
	for _, ap := range []string{ap1, ap5} {
		if box.WiFiPolicy(ap) != domain.WiFiModeAllow {
			t.Errorf("%s = %s, want Allow", ap, box.WiFiPolicy(ap))
		}
		if got := listed(box, ap); !slices.Equal(got, []string{phone, relay}) {
			t.Errorf("%s lists %v, want this phone and the relay", ap, got)
		}
		if !box.Admits(ap, phone) || !box.Admits(ap, relay) {
			t.Errorf("%s refuses this phone or the relay", ap)
		}
		for _, m := range []string{stranger, denied1, theirs} {
			if box.Admits(ap, m) {
				t.Errorf("%s admits %s", ap, m)
			}
		}
	}
	if box.WiFiPolicy(ap2) != domain.WiFiModeDisabled {
		t.Errorf("AP2 = %s: an AP outside the target was written", box.WiFiPolicy(ap2))
	}
	if !associated(box, phone) || associated(box, stranger) {
		t.Errorf("phone associated=%v, stranger associated=%v", associated(box, phone), associated(box, stranger))
	}
	for _, r := range box.WiFiRules() {
		if r.Name != wlanRuleName(mustMACv(r.MAC), r.AP) {
			t.Errorf("rule %+v is not named by kergui", r)
		}
	}
}

// TestApplyWiFiLock_CapturedTable: the 63 refusals read on the real box (21
// devices on AP1, AP5 and AP7 in Ban, ids with holes or renumbered) become an
// allow list of three devices on the three APs, in the real 64-rule table.
func TestApplyWiFiLock_CapturedTable(t *testing.T) {
	for _, holes := range []bool{false, true} {
		t.Run(fmt.Sprintf("holes=%v", holes), func(t *testing.T) {
			box := mockrouter.NewF6600P("admin", "admin")
			box.LoadCapturedWiFiACL()
			if holes {
				box.KeepWiFiRuleIDs()
			}
			box.SetRuleLimits(0, mockrouter.F6600PWiFiRuleLimit)
			refused := strings.ToLower(box.WiFiRules()[0].MAC)
			base, _ := wiredBox(t, box)
			gw := gatewayAt(t, base, port.RouterOptions{})
			aps := []string{ap1, ap5, ap7}
			if err := gw.ApplyWiFiLock(context.Background(), closeTo(aps, phone, relay, laptop)); err != nil {
				t.Fatalf("ApplyWiFiLock: %v", err)
			}
			noHazards(t, box)
			if box.WiFiRuleCount() != 9 {
				t.Errorf("%d rules, want 3 devices x 3 APs", box.WiFiRuleCount())
			}
			for _, ap := range aps {
				if box.WiFiPolicy(ap) != domain.WiFiModeAllow || !box.Admits(ap, phone) || box.Admits(ap, refused) {
					t.Errorf("%s: mode %s, phone admitted %v, %s admitted %v", ap, box.WiFiPolicy(ap), box.Admits(ap, phone), refused, box.Admits(ap, refused))
				}
			}
		})
	}
}

// TestApplyWiFiLock_ListFullBeforeAnyWrite: a target the table cannot hold
// is refused with the numbers before anything is written (D3, D4 step 1).
func TestApplyWiFiLock_ListFullBeforeAnyWrite(t *testing.T) {
	t.Run("captured table, 3 APs", func(t *testing.T) {
		box := mockrouter.NewF6600P("admin", "admin")
		box.LoadCapturedWiFiACL()
		base, _ := wiredBox(t, box)
		gw := gatewayAt(t, base, port.RouterOptions{})
		var macs []string
		for i := 0; i < 22; i++ {
			macs = append(macs, fmt.Sprintf("02:aa:bb:00:04:%02x", i))
		}
		err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5, ap7}, macs...))
		var full *domain.AccessListFullError
		if !errors.As(err, &full) || full.Kind != domain.AccessListWiFi || full.Limit != 21 || full.Used != 22 {
			t.Fatalf("ApplyWiFiLock = %v, want wifi full 22 for 21 places", err)
		}
		if box.WiFiWrites() != 0 {
			t.Errorf("%d Wi-Fi writes before refusing", box.WiFiWrites())
		}
	})
	t.Run("rules kept on an open AP", func(t *testing.T) {
		box := mockrouter.NewF6600P("admin", "admin")
		for i := 0; i < 60; i++ {
			m := fmt.Sprintf("02:aa:bb:00:05:%02x", i)
			box.AddWiFiRule(m, ap2, wlanRuleName(mustMACv(m), ap2))
		}
		base, _ := wiredBox(t, box)
		gw := gatewayAt(t, base, port.RouterOptions{})
		err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5}, phone, relay, laptop))
		var full *domain.AccessListFullError
		if !errors.As(err, &full) || full.Limit != 2 || full.Used != 3 {
			t.Fatalf("ApplyWiFiLock = %v, want 3 devices for 2 places", err)
		}
		if box.WiFiWrites() != 0 {
			t.Errorf("%d Wi-Fi writes before refusing", box.WiFiWrites())
		}
	})
}

// TestApplyWiFiLock_BoxAnswersFull: a box smaller than the adapter thinks
// answers « plein » midway: the adapter says so in devices, and the box is
// back as it was.
func TestApplyWiFiLock_BoxAnswersFull(t *testing.T) {
	box := banBox()
	box.SetRuleLimits(0, 5)
	before := stateOf(box)
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5}, phone, relay, laptop))
	var full *domain.AccessListFullError
	if !errors.As(err, &full) || full.Kind != domain.AccessListWiFi || full.Limit != 2 || full.Used != 3 {
		t.Fatalf("ApplyWiFiLock = %v, want wifi full: 3 devices for 2 places", err)
	}
	if after := stateOf(box); !sameState(after, before) {
		t.Errorf("box not back to the snapshot:\n got %v\nwant %v", after, before)
	}
	noHazards(t, box)
}

// TestApplyWiFiLock_RollsBackAtEachStep: whichever write the box refuses, the
// box ends as it was, and no write on the way read a list the wrong way.
func TestApplyWiFiLock_RollsBackAtEachStep(t *testing.T) {
	cases := []struct {
		name   string
		box    func() *mockrouter.F6600P
		target domain.WiFiLockTarget
	}{
		{"close from refusals", banBox, closeTo([]string{ap1, ap5}, phone, relay)},
		{"rewrite while closed", func() *mockrouter.F6600P { return allowBox(phone, denied1) }, closeTo([]string{ap1, ap5}, phone, relay)},
		{"whole list replaced while closed", func() *mockrouter.F6600P { return allowBox(denied1) }, closeTo([]string{ap1, ap5}, phone, relay)},
		{"close from open light", func() *mockrouter.F6600P { return lightBox(phone, denied1) }, closeTo([]string{ap1, ap5}, phone, relay)},
		{"open with refusals", func() *mockrouter.F6600P { return allowBox(phone, relay) }, domain.WiFiLockTarget{
			APs: []string{ap1, ap5}, Mode: domain.WiFiModeBan, MACs: []domain.MAC{mustMACv(denied1)},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// How many writes the run takes when nothing fails.
			ok := c.box()
			base, _ := wiredBox(t, ok)
			if err := gatewayAt(t, base, port.RouterOptions{}).ApplyWiFiLock(context.Background(), c.target); err != nil {
				t.Fatalf("ApplyWiFiLock: %v", err)
			}
			noHazards(t, ok)
			steps := ok.WiFiWrites()
			if steps < 2 {
				t.Fatalf("only %d writes", steps)
			}
			for k := 1; k <= steps; k++ {
				box := c.box()
				before := stateOf(box)
				box.FailWiFiWrite(k)
				base, _ := wiredBox(t, box)
				err := gatewayAt(t, base, port.RouterOptions{}).ApplyWiFiLock(context.Background(), c.target)
				if err == nil {
					t.Fatalf("write %d/%d refused, ApplyWiFiLock = nil", k, steps)
				}
				if after := stateOf(box); !sameState(after, before) {
					t.Errorf("write %d/%d refused: box not back to the snapshot:\n got %v\nwant %v", k, steps, after, before)
				}
				if h := box.WiFiHazards(); len(h) != 0 {
					t.Errorf("write %d/%d refused: %v", k, steps, h)
				}
			}
		})
	}
}

// TestApplyWiFiLock_ThisPhoneMustBeOnTheBox: a box that answers yes to this
// phone's rule and keeps none is caught by the read-back: the APs never
// switch to Allow, and the box is back as it was.
func TestApplyWiFiLock_ThisPhoneMustBeOnTheBox(t *testing.T) {
	box := banBox()
	box.LoseWiFiRules(phone)
	before := stateOf(box)
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5}, phone, relay))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), phone) {
		t.Fatalf("ApplyWiFiLock = %v, want a read-back failure naming this phone", err)
	}
	if after := stateOf(box); !sameState(after, before) {
		t.Errorf("box not back to the snapshot:\n got %v\nwant %v", after, before)
	}
	if !associated(box, phone) {
		t.Error("this phone was dropped")
	}
	noHazards(t, box)
}

// TestApplyWiFiLock_InterruptedRun (#94): the relay closes the Wi-Fi, the
// owner's phone takes the box's one session midway and the relay yields.
// Whatever write it stopped after, the owner's next writer finishes the close,
// or undoes it, and nothing on the way read a list the wrong way.
func TestApplyWiFiLock_InterruptedRun(t *testing.T) {
	target := closeTo([]string{ap1, ap5}, phone, relay)
	ok := banBox()
	okBase, _ := wiredBox(t, ok)
	if err := gatewayAt(t, okBase, port.RouterOptions{}).ApplyWiFiLock(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	steps := ok.WiFiWrites()

	for _, undo := range []bool{false, true} {
		for k := 1; k < steps; k++ {
			box := banBox()
			box.EnableParking()
			base, wire := wiredBox(t, box)
			var mu sync.Mutex
			yield := false
			relayGW := gatewayAt(t, base, port.RouterOptions{Preempt: func(string) bool {
				mu.Lock()
				defer mu.Unlock()
				return !yield
			}})
			wire.after = func(n int) {
				if n == k {
					box.HoldSession(ownerAtHome)
					mu.Lock()
					yield = true
					mu.Unlock()
				}
			}
			err := relayGW.ApplyWiFiLock(context.Background(), target)
			if !errors.Is(err, domain.ErrSessionHeld) {
				t.Fatalf("stopped after write %d: ApplyWiFiLock = %v, want ErrSessionHeld", k, err)
			}
			if box.WiFiWrites() != k {
				t.Fatalf("the relay wrote %d times after yielding at %d", box.WiFiWrites(), k)
			}
			wire.after = nil

			owner := gatewayAt(t, base, port.RouterOptions{})
			next := target
			if undo {
				next = domain.WiFiLockTarget{APs: []string{ap1, ap5}, Mode: domain.WiFiModeBan, MACs: []domain.MAC{mustMACv(denied1), mustMACv(denied2)}}
			}
			if err := owner.ApplyWiFiLock(context.Background(), next); err != nil {
				t.Fatalf("stopped after write %d, undo=%v: the next writer failed: %v", k, undo, err)
			}
			for _, ap := range []string{ap1, ap5} {
				want := []string{phone, relay}
				if undo {
					want = []string{denied1, denied2}
				}
				if got := listed(box, ap); box.WiFiPolicy(ap) != next.Mode || !slices.Equal(got, want) {
					t.Errorf("stopped after write %d, undo=%v: %s = %s %v, want %s %v", k, undo, ap, box.WiFiPolicy(ap), got, next.Mode, want)
				}
			}
			if h := box.WiFiHazards(); len(h) != 0 {
				t.Errorf("stopped after write %d, undo=%v: %v", k, undo, h)
			}
		}
	}
}

// TestApplyWiFiLock_RewriteWhileClosedAddsFirst: a closed AP is rewritten in
// Allow, never opened on the way, the new device added before the old one
// goes, even with the 64-rule table full.
func TestApplyWiFiLock_RewriteWhileClosedAddsFirst(t *testing.T) {
	macs := []string{phone, relay}
	for i := 0; len(macs) < 32; i++ {
		macs = append(macs, fmt.Sprintf("02:aa:bb:00:06:%02x", i))
	}
	box := allowBox(macs...)
	box.SetRuleLimits(0, mockrouter.F6600PWiFiRuleLimit)
	if box.WiFiRuleCount() != 64 {
		t.Fatalf("%d rules, want a full table", box.WiFiRuleCount())
	}
	base, wire := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	want := append([]string{laptop}, macs[:31]...) // the last one leaves, the laptop comes
	if err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5}, want...)); err != nil {
		t.Fatalf("ApplyWiFiLock: %v", err)
	}
	if n := wire.policyWrites(); n != 0 {
		t.Errorf("%d mode writes: a closed AP must be rewritten in Allow", n)
	}
	slices.Sort(want)
	for _, ap := range []string{ap1, ap5} {
		if got := listed(box, ap); !slices.Equal(got, want) || box.WiFiPolicy(ap) != domain.WiFiModeAllow {
			t.Errorf("%s = %s %v", ap, box.WiFiPolicy(ap), got)
		}
	}
	noHazards(t, box)
}

// TestApplyWiFiLock_OpenLightOnlyTheDifference: from Disabled with the list
// already there, a close writes the difference and the mode, nothing else.
func TestApplyWiFiLock_OpenLightOnlyTheDifference(t *testing.T) {
	box := lightBox(phone, relay)
	base, wire := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	if err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1, ap5}, phone, relay, laptop)); err != nil {
		t.Fatal(err)
	}
	if got := box.WiFiWrites(); got != 3 {
		t.Errorf("%d writes %v, want 2 adds and 1 mode", got, wire.writes)
	}
	noHazards(t, box)
}

// TestApplyWiFiLock_DisabledKeepsTheRules: the open-light target leaves the
// list on the APs, filtering nothing.
func TestApplyWiFiLock_DisabledKeepsTheRules(t *testing.T) {
	box := allowBox(phone, relay)
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	target := closeTo([]string{ap1, ap5}, phone, relay)
	target.Mode = domain.WiFiModeDisabled
	if err := gw.ApplyWiFiLock(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	for _, ap := range []string{ap1, ap5} {
		if got := listed(box, ap); box.WiFiPolicy(ap) != domain.WiFiModeDisabled || !slices.Equal(got, []string{phone, relay}) || !box.Admits(ap, stranger) {
			t.Errorf("%s = %s %v", ap, box.WiFiPolicy(ap), got)
		}
	}
	noHazards(t, box)
}

// TestApplyWiFiLock_RefusesAnInvalidTarget: nothing is written for a target
// the domain refuses, or an AP the box does not have.
func TestApplyWiFiLock_RefusesAnInvalidTarget(t *testing.T) {
	box := banBox()
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	if err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{ap1})); !errors.Is(err, domain.ErrEmptyAllowList) {
		t.Errorf("empty allow list: %v", err)
	}
	if err := gw.ApplyWiFiLock(context.Background(), closeTo([]string{"DEV.WIFI.AP9"}, phone)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("unknown AP: %v", err)
	}
	if box.WiFiWrites() != 0 {
		t.Errorf("%d writes", box.WiFiWrites())
	}
}

// TestSetWiFiMode: open light, add window and re-close are one mode write
// each, the rules kept; Allow on an AP with no rule, and a flip that would
// read the rules the other way, are refused before any write.
func TestSetWiFiMode(t *testing.T) {
	box := allowBox(phone, relay)
	before := box.WiFiRules()
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	ctx := context.Background()
	aps := []string{ap1, ap5}

	if err := gw.SetWiFiMode(ctx, aps, domain.WiFiModeDisabled); err != nil {
		t.Fatal(err)
	}
	if !box.Admits(ap1, stranger) || box.WiFiPolicy(ap5) != domain.WiFiModeDisabled {
		t.Error("Disabled does not let a new device in")
	}
	box.Join(mockrouter.Station{MAC: stranger, AP: ap1})
	if err := gw.SetWiFiMode(ctx, aps, domain.WiFiModeAllow); err != nil {
		t.Fatal(err)
	}
	if box.Admits(ap1, stranger) || !box.Admits(ap1, phone) || associated(box, stranger) {
		t.Error("the re-close does not filter")
	}
	if !slices.Equal(box.WiFiRules(), before) || box.WiFiWrites() != 2 {
		t.Errorf("rules %v after %d writes, want them untouched by 2 mode writes", box.WiFiRules(), box.WiFiWrites())
	}

	if err := gw.SetWiFiMode(ctx, []string{ap2}, domain.WiFiModeAllow); !errors.Is(err, domain.ErrEmptyAllowList) {
		t.Errorf("Allow on an AP with no rule: %v", err)
	}
	if err := gw.SetWiFiMode(ctx, aps, domain.WiFiModeBan); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("Allow straight to Ban: %v", err)
	}
	if err := gw.SetWiFiMode(ctx, aps, "Open"); !errors.Is(err, domain.ErrInvalidAccessMode) {
		t.Errorf("unknown mode: %v", err)
	}
	if box.WiFiWrites() != 2 {
		t.Errorf("%d writes, a refused flip wrote", box.WiFiWrites())
	}
	noHazards(t, box)

	ban := banBox()
	base, _ = wiredBox(t, ban)
	gw = gatewayAt(t, base, port.RouterOptions{})
	if err := gw.SetWiFiMode(ctx, aps, domain.WiFiModeAllow); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("Ban straight to Allow (the trap): %v", err)
	}
	if ban.WiFiWrites() != 0 {
		t.Errorf("%d writes for the trap", ban.WiFiWrites())
	}
}

// TestWiFiLockBox: the enabled APs with their names, bands and modes, every
// rule of the shared table by AP, and the measured limit.
func TestWiFiLockBox(t *testing.T) {
	box := allowBox(phone, relay)
	box.AddWiFiRule(denied1, ap2, "x")
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	b, err := gw.WiFiLockBox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.WiFiLockAP{
		{ID: ap1, Band: "2.4GHz", Name: "MonWiFi-2G", Mode: domain.WiFiModeAllow},
		{ID: ap5, Band: "5GHz", Name: "MonWiFi-5G", Mode: domain.WiFiModeAllow},
	}
	if !slices.Equal(b.APs, want) {
		t.Errorf("APs = %+v, want %+v", b.APs, want)
	}
	if b.Limit != 64 || b.Used != 5 {
		t.Errorf("limit %d used %d, want 64 and 5", b.Limit, b.Used)
	}
	if len(b.Rules[ap1]) != 2 || len(b.Rules[ap5]) != 2 || len(b.Rules[ap2]) != 1 {
		t.Errorf("rules = %v", b.Rules)
	}
	if places, known := b.Places([]string{ap1, ap5}); !known || places != 31 {
		t.Errorf("places = %d %v, want (64-1)/2", places, known)
	}
}

// TestWiFiUnblock_OnlyOnBanAPs: a device allowed on AP1 in Allow and refused
// on AP7 in Ban is freed on AP7 only; its allow rule, which may be AP1's
// last, stays, and so does the allow list kept on AP5 in Disabled (open
// light).
func TestWiFiUnblock_OnlyOnBanAPs(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	box.AddWiFiRule(laptop, ap1, wlanRuleName(mustMACv(laptop), ap1))
	box.SetWiFiPolicy(ap1, domain.WiFiModeAllow)
	box.AddWiFiRule(laptop, ap5, wlanRuleName(mustMACv(laptop), ap5))
	box.SetWiFiPolicy(ap7, domain.WiFiModeBan)
	box.AddWiFiRule(laptop, ap7, wlanRuleName(mustMACv(laptop), ap7))
	base, _ := wiredBox(t, box)
	gw := gatewayAt(t, base, port.RouterOptions{})
	if err := gw.WiFiUnblock(context.Background(), mustMACv(laptop)); err != nil {
		t.Fatal(err)
	}
	if got := listed(box, ap1); !slices.Equal(got, []string{laptop}) {
		t.Errorf("AP1 lists %v: the allow rule was removed", got)
	}
	if got := listed(box, ap5); !slices.Equal(got, []string{laptop}) {
		t.Errorf("AP5 lists %v: the allow list kept in Disabled was touched", got)
	}
	if got := listed(box, ap7); len(got) != 0 {
		t.Errorf("AP7 still refuses %v", got)
	}
	if !box.Admits(ap1, laptop) {
		t.Error("AP1 in Allow no longer admits the laptop")
	}
	noHazards(t, box)
}

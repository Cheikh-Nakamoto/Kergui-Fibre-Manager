package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// #115: the app no longer has a switch for the box's filter. Kergui keeps it
// on itself: whoever reads the box (the phone, the relay) switches it back on,
// in blocklist mode, when it finds it off while kergui's blocks are on the
// box. On the F6600P mock and a fake clock.

const keptOnLine = "Kergui a rallumé les blocages de la box"

// newKeepOnRig is the purge rig with the filter interactor on the same box
// and clock. The box holds the household's two captured rules.
func newKeepOnRig(t *testing.T) (*purgeRig, *usecase.MACFilter) {
	t.Helper()
	r := newPurgeRig(t)
	uc := usecase.NewMACFilter(r.factory, r.vault, r.disco, r.log).WithClock(r.clock)
	return r, uc
}

// TestKeepOn_SwitchesBackOnWhenKerguiBlocks: the filter is off while a rule
// kergui wrote is on the box: it is switched back on, in Discard, with one
// journal line.
func TestKeepOn_SwitchesBackOnWhenKerguiBlocks(t *testing.T) {
	r, uc := newKeepOnRig(t)
	r.box.AddFirewallRule("02:aa:bb:00:00:01", "02:aa:bb:00:00:01")
	r.box.SetMACFilter(false, "Discard")

	on, err := uc.KeepOnIfDue(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Error("KeepOnIfDue = false, want true (switched back on)")
	}
	if !r.box.MACFilterEnabled() || r.box.MACFilterMode() != "Discard" {
		t.Fatalf("box filter on=%v mode=%s, want on Discard", r.box.MACFilterEnabled(), r.box.MACFilterMode())
	}
	if n := r.log.count(keptOnLine); n != 1 {
		t.Errorf("%d journal lines %q, want 1: %v", n, keptOnLine, r.log.lines)
	}
}

// TestKeepOn_NeverPermit: a filter left off in allowlist mode comes back on in
// blocklist mode: Permit would cut every unlisted device.
func TestKeepOn_NeverPermit(t *testing.T) {
	r, uc := newKeepOnRig(t)
	r.box.AddFirewallRule("02:aa:bb:00:00:01", "02:aa:bb:00:00:01")
	r.box.SetMACFilter(false, "Permit")

	if _, err := uc.KeepOnIfDue(context.Background(), r.in); err != nil {
		t.Fatal(err)
	}
	if !r.box.MACFilterEnabled() || r.box.MACFilterMode() != "Discard" {
		t.Fatalf("box filter on=%v mode=%s, want on Discard", r.box.MACFilterEnabled(), r.box.MACFilterMode())
	}
}

// TestKeepOn_HouseholdRulesOnly: with only the rules the household typed in
// the box's page, the household's own switch is left as they set it.
func TestKeepOn_HouseholdRulesOnly(t *testing.T) {
	r, uc := newKeepOnRig(t)
	r.box.SetMACFilter(false, "Discard")

	on, err := uc.KeepOnIfDue(context.Background(), r.in)
	if err != nil || on {
		t.Fatalf("KeepOnIfDue = %v, %v; want false, nil", on, err)
	}
	if r.box.MACFilterEnabled() {
		t.Error("the filter was switched on with no rule of kergui's on the box")
	}
	if n := r.log.count(keptOnLine); n != 0 {
		t.Errorf("journal says %q with nothing done", keptOnLine)
	}
}

// TestKeepOn_LeavesAnOnFilter: on, in either mode, nothing is written.
func TestKeepOn_LeavesAnOnFilter(t *testing.T) {
	for _, mode := range []string{"Discard", "Permit"} {
		r, uc := newKeepOnRig(t)
		r.box.AddFirewallRule("02:aa:bb:00:00:01", "02:aa:bb:00:00:01")
		r.box.SetMACFilter(true, mode)

		on, err := uc.KeepOnIfDue(context.Background(), r.in)
		if err != nil || on {
			t.Fatalf("%s: KeepOnIfDue = %v, %v; want false, nil", mode, on, err)
		}
		if !r.box.MACFilterEnabled() || r.box.MACFilterMode() != mode {
			t.Errorf("%s: box filter on=%v mode=%s, want untouched", mode, r.box.MACFilterEnabled(), r.box.MACFilterMode())
		}
	}
}

// TestKeepOn_Paced: the switch is read at most every FilterCheckEvery per
// box, since the phone and the relay list the devices every 30 s.
func TestKeepOn_Paced(t *testing.T) {
	r, uc := newKeepOnRig(t)
	r.box.AddFirewallRule("02:aa:bb:00:00:01", "02:aa:bb:00:00:01")
	r.box.SetMACFilter(false, "Discard")
	ctx := context.Background()
	if _, err := uc.KeepOnIfDue(ctx, r.in); err != nil {
		t.Fatal(err)
	}

	// Switched off again in the box's page.
	r.box.SetMACFilter(false, "Discard")
	r.clock.t = r.clock.t.Add(usecase.FilterCheckEvery - time.Second)
	if on, err := uc.KeepOnIfDue(ctx, r.in); err != nil || on {
		t.Fatalf("before the pace: KeepOnIfDue = %v, %v; want false, nil", on, err)
	}
	if r.box.MACFilterEnabled() {
		t.Fatal("the switch was read again before FilterCheckEvery")
	}

	r.clock.t = r.clock.t.Add(time.Second)
	if on, err := uc.KeepOnIfDue(ctx, r.in); err != nil || !on {
		t.Fatalf("after the pace: KeepOnIfDue = %v, %v; want true, nil", on, err)
	}
	if !r.box.MACFilterEnabled() {
		t.Error("the filter was not switched back on after FilterCheckEvery")
	}
	if n := r.log.count(keptOnLine); n != 2 {
		t.Errorf("%d journal lines %q, want 2", n, keptOnLine)
	}
}

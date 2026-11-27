package mockrouter

import (
	"strings"
	"testing"
)

// The mock's Wi-Fi access control in Allow (RFC-007 D4, D7, W2-W4): one
// shared table, rules that mean the opposite in Ban and in Allow, unlisted
// stations dropped on the switch to Allow, and the two quirks measured live.

const (
	lockAP   = "DEV.WIFI.AP1"
	phoneMAC = "02:aa:bb:00:00:01"
	otherMAC = "02:aa:bb:00:00:02"
)

func TestWiFiLock_RulesInvertBetweenBanAndAllow(t *testing.T) {
	s := NewF6600P("", "")
	s.AddWiFiRule(phoneMAC, lockAP, phoneMAC+" AP1")
	if !s.Admits(lockAP, phoneMAC) || !s.Admits(lockAP, otherMAC) {
		t.Fatal("Disabled must admit everyone, listed or not")
	}
	s.SetWiFiPolicy(lockAP, "Ban")
	if s.Admits(lockAP, phoneMAC) || !s.Admits(lockAP, otherMAC) {
		t.Fatal("Ban must refuse the listed device only")
	}
	s.SetWiFiPolicy(lockAP, "Disabled")
	s.SetWiFiPolicy(lockAP, "Allow")
	if !s.Admits(lockAP, phoneMAC) || s.Admits(lockAP, otherMAC) {
		t.Fatal("Allow must admit the listed device only")
	}
	if len(s.WiFiRules()) != 1 {
		t.Errorf("a mode switch touched the rules: %+v", s.WiFiRules())
	}
	if h := s.WiFiHazards(); len(h) != 0 {
		t.Errorf("hazards through Disabled: %v", h)
	}
}

// TestWiFiLock_TheTrapIsRecorded: Ban straight to Allow (or back) with rules
// on the AP reads every refusal as an admission (RFC-007 « The trap »).
func TestWiFiLock_TheTrapIsRecorded(t *testing.T) {
	s := NewF6600P("", "")
	s.SetWiFiPolicy(lockAP, "Ban")
	s.AddWiFiRule(otherMAC, lockAP, otherMAC+" AP1")
	s.SetWiFiPolicy(lockAP, "Allow")
	h := s.WiFiHazards()
	if len(h) != 1 || !strings.Contains(h[0], "Ban to Allow") {
		t.Fatalf("hazards = %v, want the trap", h)
	}
	s.SetWiFiPolicy(lockAP, "Ban")
	if h := s.WiFiHazards(); len(h) != 2 || !strings.Contains(h[1], "Allow to Ban") {
		t.Fatalf("hazards = %v, want the reverse trap too", h)
	}
}

// TestWiFiLock_AllowWithEmptyListHasNoEffect (W2): the box takes the mode but
// filters nothing, and a rule added afterwards does not take effect.
func TestWiFiLock_AllowWithEmptyListHasNoEffect(t *testing.T) {
	s := NewF6600P("", "")
	s.SetWiFiPolicy(lockAP, "Allow")
	if s.WiFiPolicy(lockAP) != "Allow" {
		t.Fatalf("mode = %s, the box accepts it", s.WiFiPolicy(lockAP))
	}
	s.AddWiFiRule(phoneMAC, lockAP, phoneMAC+" AP1")
	if !s.Admits(lockAP, otherMAC) {
		t.Error("an Allow switched on with an empty list refused a stranger")
	}
	if h := s.WiFiHazards(); len(h) != 1 || !strings.Contains(h[0], "empty") {
		t.Errorf("hazards = %v, want the empty Allow", h)
	}
	// A flip through Disabled makes it take effect (W4: no restart needed).
	s.SetWiFiPolicy(lockAP, "Disabled")
	s.SetWiFiPolicy(lockAP, "Allow")
	if s.Admits(lockAP, otherMAC) || !s.Admits(lockAP, phoneMAC) {
		t.Error("Allow with a rule does not filter")
	}
}

// TestWiFiLock_LastRuleRemovedInAllowRefusesEveryone (W2 bis).
func TestWiFiLock_LastRuleRemovedInAllowRefusesEveryone(t *testing.T) {
	s := NewF6600P("", "")
	s.AddWiFiRule(phoneMAC, lockAP, phoneMAC+" AP1")
	s.SetWiFiPolicy(lockAP, "Allow")
	s.RemoveWiFiRule(phoneMAC, lockAP)
	if s.Admits(lockAP, phoneMAC) || s.Admits(lockAP, otherMAC) {
		t.Error("an AP in Allow with its last rule removed must refuse everyone")
	}
	if h := s.WiFiHazards(); len(h) != 1 || !strings.Contains(h[0], "last rule") {
		t.Errorf("hazards = %v, want the last rule removed", h)
	}
}

// TestWiFiLock_UnlistedStationsDropped (W3): a station on the AP and not in
// the list leaves the Wi-Fi on the switch to Allow, listed ones stay, those
// on another AP too; removing a station's rule in Allow drops it.
func TestWiFiLock_UnlistedStationsDropped(t *testing.T) {
	s := NewF6600P("", "")
	const away = "02:aa:bb:00:00:03"
	s.Join(Station{MAC: phoneMAC, AP: lockAP})
	s.Join(Station{MAC: otherMAC, AP: lockAP})
	s.Join(Station{MAC: away, AP: "DEV.WIFI.AP5"})
	s.AddWiFiRule(phoneMAC, lockAP, phoneMAC+" AP1")
	s.AddWiFiRule(otherMAC, lockAP, otherMAC+" AP1")
	s.SetWiFiPolicy(lockAP, "Allow")
	s.RemoveWiFiRule(otherMAC, lockAP)
	want := map[string]bool{phoneMAC: true, otherMAC: false, away: true}
	for _, st := range s.Stations() {
		if w, ok := want[st.MAC]; ok && st.Associated != w {
			t.Errorf("%s associated = %v, want %v", st.MAC, st.Associated, w)
		}
	}
	s.Join(Station{MAC: "02:aa:bb:00:00:04", AP: lockAP})
	s.SetWiFiPolicy(lockAP, "Disabled")
	s.SetWiFiPolicy(lockAP, "Allow")
	for _, st := range s.Stations() {
		if st.MAC == "02:aa:bb:00:00:04" && st.Associated {
			t.Error("a stranger who joined during Disabled stayed after the switch back to Allow")
		}
	}
}

// TestWiFiLock_CapturedTableAndModes: the table read on the real box loads,
// holes in its ids included, and its three APs are in Ban.
func TestWiFiLock_CapturedTableAndModes(t *testing.T) {
	s := NewF6600P("", "")
	s.LoadCapturedWiFiACL()
	s.KeepWiFiRuleIDs()
	for _, ap := range []string{"DEV.WIFI.AP1", "DEV.WIFI.AP5", "DEV.WIFI.AP7"} {
		if s.WiFiPolicy(ap) != "Ban" {
			t.Errorf("%s = %s, want Ban", ap, s.WiFiPolicy(ap))
		}
	}
	if s.WiFiRuleCount() != 63 {
		t.Errorf("%d rules, want 63", s.WiFiRuleCount())
	}
	page := s.renderWiFiRules()
	if strings.Contains(page, "<ParaValue>DEV.ACL40</ParaValue>") {
		t.Error("the captured hole at DEV.ACL40 was filled")
	}
}

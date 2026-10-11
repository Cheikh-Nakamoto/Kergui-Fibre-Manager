package domain

import (
	"testing"
	"time"
)

func TestIsRandomized(t *testing.T) {
	for mac, want := range map[string]bool{
		"9a:35:a2:d2:24:15": true,  // 0x9a: locally administered
		"ba:62:35:2b:be:2f": true,  // 0xba
		"bc:a8:a6:79:d9:a7": false, // 0xbc: factory address
		"14:ac:60:bc:50:f5": false,
	} {
		if got := MustMAC(mac).IsRandomized(); got != want {
			t.Errorf("%s IsRandomized = %v, want %v", mac, got, want)
		}
	}
}

func TestIdentityKey(t *testing.T) {
	for name, want := range map[string]string{
		"OPPO-Find-X5": "oppo-find-x5", " Galaxy-S22 ": "galaxy-s22", "iPhone-de-Awa": "iphone-de-awa",
		"zone01-HP.local": "zone01-hp",
	} {
		if got, ok := IdentityKey(name); !ok || got != want {
			t.Errorf("IdentityKey(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, generic := range []string{"", "iPhone", "IPAD", "android-3f2a9c1b77e0", "9a35a2d22415", "localhost"} {
		if _, ok := IdentityKey(generic); ok {
			t.Errorf("IdentityKey(%q) must be refused as generic", generic)
		}
	}
}

func TestIdentityLosses(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := func(mac string, day int) Sighting {
		return Sighting{MAC: MustMAC(mac), FirstSeen: base.AddDate(0, 0, day)}
	}
	group := []Sighting{
		s("9a:00:00:00:00:04", 20), s("ba:00:00:00:00:01", 0), // first address: not a loss
		s("9a:00:00:00:00:02", 5), s("da:00:00:00:00:03", 12),
		s("14:00:00:00:00:05", 25), // factory address: switching to it is the fix, not a loss
	}
	if got := IdentityLosses(group, base); got != 3 {
		t.Errorf("losses = %d, want 3", got)
	}
	if got := IdentityLosses(group, base.AddDate(0, 0, 10)); got != 2 {
		t.Errorf("losses since day 10 = %d, want 2", got)
	}
	if got := IdentityLosses(group[:1], base); got != 0 {
		t.Errorf("single address = %d losses", got)
	}
	st := IdentityState{BlacklistedAt: base, LiftedAt: base.Add(time.Hour)}
	if st.Blacklisted() {
		t.Error("lifted blacklist still in force")
	}
}

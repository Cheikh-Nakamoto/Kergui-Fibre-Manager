package ztef6600p

import (
	"strings"
	"testing"
)

// TestMeta_ListsWiFiAccessControl: the Wi-Fi access control the gateway
// implements (wifi.go) is documented in the adapter metadata, like block and
// unblock, so `kergui inspect` and docs show what "Couper l'accès" calls.
func TestMeta_ListsWiFiAccessControl(t *testing.T) {
	want := map[string]struct{ method, tag string }{
		"wifi_blocked": {"GET", wlanRuleTag},
		"wifi_block":   {"POST", wlanRuleTag},
		"wifi_unblock": {"POST", wlanRuleTag},
	}
	for _, e := range Meta().Endpoints {
		w, ok := want[e.Operation]
		if !ok {
			continue
		}
		if e.Method != w.method || !strings.Contains(e.Path, w.tag) {
			t.Errorf("%s = %s %s, want %s on %s", e.Operation, e.Method, e.Path, w.method, w.tag)
		}
		if e.Notes == "" {
			t.Errorf("%s has no notes", e.Operation)
		}
		delete(want, e.Operation)
	}
	for op := range want {
		t.Errorf("operation %s missing from Meta().Endpoints", op)
	}
}

// TestMeta_SSIDSetVerified (#34): the owner validated the SSID write (on/off,
// name, password) on a live F6600P in 2026-11; the Wi-Fi access control
// entries are not validated yet and stay unverified.
func TestMeta_SSIDSetVerified(t *testing.T) {
	seen := false
	for _, e := range Meta().Endpoints {
		switch e.Operation {
		case "wifi_ssid_set":
			seen = true
			if !e.Verified || e.Method != "POST" || !strings.Contains(e.Path, wlanSSIDTag) {
				t.Errorf("wifi_ssid_set = %+v, want a verified POST on %s", e, wlanSSIDTag)
			}
			if !strings.Contains(e.Notes, "2026-11") {
				t.Errorf("wifi_ssid_set notes %q do not date the live validation", e.Notes)
			}
		case "wifi_blocked", "wifi_unblock":
			if !e.Verified || !strings.Contains(e.Notes, "2026-11-27") {
				t.Errorf("%s = %+v, want verified with the date of the live session (RFC-007 W8)", e.Operation, e)
			}
		case "wifi_block":
			if e.Verified {
				t.Errorf("wifi_block is marked verified, but WiFiBlock itself has not run on a live box")
			}
		}
	}
	if !seen {
		t.Error("wifi_ssid_set missing from Meta().Endpoints")
	}
}
